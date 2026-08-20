package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T, state State) *App {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "master.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(target *State) error { *target = state; return nil }); err != nil {
		t.Fatal(err)
	}
	return NewApp(Config{BaseURL: "https://master.example.org"}, store)
}

func TestOverviewDoesNotExposeSecrets(t *testing.T) {
	app := newTestApp(t, State{Version: stateVersion, Servers: []Server{{
		ID: "server-1", Address: "192.0.2.1:22", DuckDNSURL: "one.duckdns.org",
		SSHPrivateKey: "PRIVATE", SSHPassphrase: "PASSPHRASE", DuckDNSToken: "TOKEN", APIUsername: "admin", APIPassword: "PASSWORD",
	}}, Users: []User{}})
	request := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", response.Code)
	}
	for _, secret := range []string{"PRIVATE", "PASSPHRASE", "TOKEN", "PASSWORD"} {
		if strings.Contains(body, secret) {
			t.Fatalf("secret %q leaked into overview", secret)
		}
	}
}

func TestSubscriptionPlainTextAndNotFound(t *testing.T) {
	app := newTestApp(t, State{Version: stateVersion,
		Servers: []Server{{ID: "one"}, {ID: "two"}},
		Users: []User{{ID: "user", SubscriptionToken: "valid-token", Links: map[string]UserLink{
			"one": {ServerID: "one", Status: "ready", URI: "vless://first"},
			"two": {ServerID: "two", Status: "error", URI: "vless://hidden"},
		}, CreatedAt: time.Now()}},
	})
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/subscribe/valid-token", nil))
	if response.Code != http.StatusOK || response.Body.String() != "vless://first\n" {
		t.Fatalf("unexpected subscription: %d %q", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("unexpected content type %s", contentType)
	}
	missing := httptest.NewRecorder()
	app.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/subscribe/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unexpected missing status %d", missing.Code)
	}
}

func TestEmbeddedUIAndClipboardFallback(t *testing.T) {
	app := newTestApp(t, State{Version: stateVersion, Servers: []Server{}, Users: []User{}})
	index := httptest.NewRecorder()
	app.Handler().ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "Серверы") || !strings.Contains(index.Body.String(), "Пользователи") {
		t.Fatalf("unexpected embedded index: %d", index.Code)
	}
	for _, removed := range []string{`class="topbar"`, `class="hero"`, "Синхронизировать всё"} {
		if strings.Contains(index.Body.String(), removed) {
			t.Fatalf("removed header content %q is still embedded", removed)
		}
	}
	if !strings.Contains(index.Body.String(), "Синхронизировать пользователей") {
		t.Fatal("user synchronization action is missing from embedded UI")
	}
	for _, action := range []string{"Закрыть", "Выполнить удаление", "Попробовать ещё раз"} {
		if !strings.Contains(index.Body.String(), action) {
			t.Fatalf("failed provisioning action %q is missing", action)
		}
	}
	javascript := httptest.NewRecorder()
	app.Handler().ServeHTTP(javascript, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if javascript.Code != http.StatusOK || !strings.Contains(javascript.Body.String(), "navigator.clipboard") || !strings.Contains(javascript.Body.String(), "copy-fallback") {
		t.Fatalf("clipboard fallback is missing from embedded UI")
	}
	if !strings.Contains(javascript.Body.String(), "/api/servers/cleanup") || !strings.Contains(javascript.Body.String(), "retryServerInput") {
		t.Fatal("failed provisioning cleanup or retry behavior is missing")
	}
	if !strings.Contains(javascript.Body.String(), "Ubuntu 22.04, 24.04 или 26.04") {
		t.Fatal("supported Ubuntu LTS versions are missing from embedded UI")
	}
	stylesheet := httptest.NewRecorder()
	app.Handler().ServeHTTP(stylesheet, httptest.NewRequest(http.MethodGet, "/assets/app.css", nil))
	if stylesheet.Code != http.StatusOK || !strings.Contains(stylesheet.Body.String(), "overflow-x: hidden") {
		t.Fatal("dialog horizontal overflow protection is missing")
	}
}

func TestCleanupServerHTTPStartsJobWithoutReturningSecrets(t *testing.T) {
	privateKey, _ := testPrivateKey(t)
	app := newTestApp(t, State{Version: stateVersion, Servers: []Server{}, Users: []User{}})
	app.config.SlaveUninstallURL = "https://example.org/uninstall.sh"
	body, err := json.Marshal(AddServerRequest{
		Address: "127.0.0.1:1", PrivateKey: privateKey,
		DuckDNSURL: "cleanup.duckdns.org", DuckDNSToken: "duck-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/servers/cleanup", strings.NewReader(string(body))))
	if response.Code != http.StatusAccepted {
		t.Fatalf("cleanup returned %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "duck-secret") || strings.Contains(response.Body.String(), "PRIVATE KEY") {
		t.Fatal("cleanup response exposed request secrets")
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	job, exists := app.jobs.Get(result["job_id"])
	if !exists {
		t.Fatal("cleanup job is missing")
	}
	waitForJob(t, job)
	if job.Snapshot().Kind != "server_cleanup" {
		t.Fatalf("unexpected job kind: %s", job.Snapshot().Kind)
	}
}

func TestUserHTTPCreateAndDelete(t *testing.T) {
	app := newTestApp(t, State{Version: stateVersion, Servers: []Server{}, Users: []User{}})
	create := httptest.NewRecorder()
	app.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"email":"Обычное имя профиля"}`)))
	if create.Code != http.StatusAccepted {
		t.Fatalf("create returned %d: %s", create.Code, create.Body.String())
	}
	var createResult map[string]string
	if err := json.Unmarshal(create.Body.Bytes(), &createResult); err != nil {
		t.Fatal(err)
	}
	createJob, exists := app.jobs.Get(createResult["job_id"])
	if !exists {
		t.Fatal("create job is missing")
	}
	waitForJob(t, createJob)
	users := app.store.Snapshot().Users
	if len(users) != 1 || users[0].Email != "Обычное имя профиля" {
		t.Fatalf("user was not created: %+v", users)
	}

	deleteResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/api/users/"+users[0].ID, strings.NewReader(`{}`)))
	if deleteResponse.Code != http.StatusAccepted {
		t.Fatalf("delete returned %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var deleteResult map[string]string
	if err := json.Unmarshal(deleteResponse.Body.Bytes(), &deleteResult); err != nil {
		t.Fatal(err)
	}
	deleteJob, exists := app.jobs.Get(deleteResult["job_id"])
	if !exists {
		t.Fatal("delete job is missing")
	}
	waitForJob(t, deleteJob)
	if len(app.store.Snapshot().Users) != 0 {
		t.Fatal("user was not deleted")
	}
}

func TestServerHTTPRejectsUnknownFields(t *testing.T) {
	app := newTestApp(t, State{Version: stateVersion, Servers: []Server{}, Users: []User{}})
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/servers", strings.NewReader(`{"unexpected":true}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", response.Code)
	}
}
