package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.URL.Scheme = transport.target.Scheme
	cloned.URL.Host = transport.target.Host
	return transport.base.RoundTrip(cloned)
}

type fakeSlave struct {
	mu      sync.Mutex
	records []SlaveRecord
	nextID  int
}

func (fake *fakeSlave) handler(response http.ResponseWriter, request *http.Request) {
	username, password, ok := request.BasicAuth()
	if !ok || username != "admin" || password != "slave-password" {
		response.WriteHeader(http.StatusUnauthorized)
		return
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	response.Header().Set("Content-Type", "application/json")
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/list":
		_ = json.NewEncoder(response).Encode(fake.records)
	case request.Method == http.MethodPost && request.URL.Path == "/register":
		var input map[string]string
		_ = json.NewDecoder(request.Body).Decode(&input)
		for _, record := range fake.records {
			if normalizeEmail(record.Email) == normalizeEmail(input["email"]) {
				response.WriteHeader(http.StatusConflict)
				_, _ = response.Write([]byte(`{"error":"duplicate"}`))
				return
			}
		}
		fake.nextID++
		record := SlaveRecord{ID: fmt.Sprintf("client-%d", fake.nextID), Email: input["email"], URI: fmt.Sprintf("vless://client-%d", fake.nextID), CreatedAt: time.Now().UTC()}
		fake.records = append(fake.records, record)
		response.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(response).Encode(record)
	case request.Method == http.MethodPost && request.URL.Path == "/remove":
		var input map[string]string
		_ = json.NewDecoder(request.Body).Decode(&input)
		removed := false
		for index := range fake.records {
			if fake.records[index].ID == input["id"] {
				fake.records = append(fake.records[:index], fake.records[index+1:]...)
				removed = true
				break
			}
		}
		_ = json.NewEncoder(response).Encode(map[string]bool{"removed": removed})
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

func TestAuthoritativeSyncAddsAndRemoves(t *testing.T) {
	fake := &fakeSlave{records: []SlaveRecord{{ID: "orphan", Email: "orphan@example.org", URI: "vless://orphan", CreatedAt: time.Now().Add(-time.Hour)}}}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(fake.handler))
	defer tlsServer.Close()
	target, _ := url.Parse(tlsServer.URL)

	store, err := NewStore(filepath.Join(t.TempDir(), "master.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.Servers = []Server{{ID: "server", DuckDNSURL: "fake.duckdns.org", APIUsername: "admin", APIPassword: "slave-password", Status: "ready"}}
		state.Users = []User{{ID: "user", Email: "master@example.org", SubscriptionToken: "token", Links: map[string]UserLink{}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp(Config{BaseURL: "https://master.example.org"}, store)
	app.slaves.httpClient = tlsServer.Client()
	app.slaves.httpClient.Transport = rewriteTransport{target: target, base: tlsServer.Client().Transport}

	job, err := app.jobs.Start("sync", nil, func(reporter *JobReporter) error {
		return app.fullSync(context.Background(), reporter)
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForJob(t, job)
	if snapshot := job.Snapshot(); snapshot.Status != "success" {
		t.Fatalf("sync failed: %+v", snapshot)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.records) != 1 || fake.records[0].Email != "master@example.org" {
		t.Fatalf("unexpected fake Slave records: %+v", fake.records)
	}
	link := store.Snapshot().Users[0].Links["server"]
	if link.Status != "ready" || link.URI == "" {
		t.Fatalf("unexpected stored link: %+v", link)
	}
}

func TestSlaveClientRejectsUnauthorized(t *testing.T) {
	fake := &fakeSlave{}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(fake.handler))
	defer tlsServer.Close()
	target, _ := url.Parse(tlsServer.URL)
	client := NewSlaveClient()
	client.httpClient = tlsServer.Client()
	client.httpClient.Transport = rewriteTransport{target: target, base: tlsServer.Client().Transport}
	_, err := client.List(context.Background(), Server{DuckDNSURL: "fake.duckdns.org", APIUsername: "wrong", APIPassword: "wrong"})
	var apiError *SlaveAPIError
	if !errors.As(err, &apiError) || apiError.Status != http.StatusUnauthorized {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateUserPersistsOnSlaveFailure(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "offline", http.StatusServiceUnavailable)
	}))
	defer tlsServer.Close()
	target, _ := url.Parse(tlsServer.URL)
	store, err := NewStore(filepath.Join(t.TempDir(), "master.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.Servers = []Server{{ID: "server", DuckDNSURL: "offline.duckdns.org", APIUsername: "admin", APIPassword: "password"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp(Config{BaseURL: "https://master.example.org"}, store)
	app.slaves.httpClient = tlsServer.Client()
	app.slaves.httpClient.Transport = rewriteTransport{target: target, base: tlsServer.Client().Transport}
	job, err := app.jobs.Start("add", nil, func(reporter *JobReporter) error {
		return app.createUser(context.Background(), "partial@example.org", reporter)
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForJob(t, job)
	if snapshot := job.Snapshot(); snapshot.Status != "success_with_warnings" {
		t.Fatalf("unexpected job: %+v", snapshot)
	}
	users := store.Snapshot().Users
	if len(users) != 1 || users[0].Links["server"].Status != "error" {
		t.Fatalf("partial user was not retained: %+v", users)
	}
}

func TestDeleteUserInvalidatesSubscriptionBeforeCleanup(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "offline", http.StatusServiceUnavailable)
	}))
	defer tlsServer.Close()
	target, _ := url.Parse(tlsServer.URL)
	store, err := NewStore(filepath.Join(t.TempDir(), "master.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.Servers = []Server{{ID: "server", DuckDNSURL: "offline.duckdns.org", APIUsername: "admin", APIPassword: "password"}}
		state.Users = []User{{ID: "user", Email: "gone@example.org", SubscriptionToken: "gone-token", Links: map[string]UserLink{}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp(Config{BaseURL: "https://master.example.org"}, store)
	app.slaves.httpClient = tlsServer.Client()
	app.slaves.httpClient.Transport = rewriteTransport{target: target, base: tlsServer.Client().Transport}
	job, err := app.jobs.Start("delete", nil, func(reporter *JobReporter) error {
		return app.deleteUser(context.Background(), "user", reporter)
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForJob(t, job)
	if len(store.Snapshot().Users) != 0 {
		t.Fatal("user remained in Master after immediate deletion")
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/subscribe/gone-token", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("deleted subscription returned %d", response.Code)
	}
}
