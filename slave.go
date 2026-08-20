package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SlaveClient struct {
	httpClient *http.Client
}

type SlaveRecord struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	URI       string    `json:"uri"`
	CreatedAt time.Time `json:"created_at"`
}

type SlaveAPIError struct {
	Status int
	Body   string
}

func (err *SlaveAPIError) Error() string {
	return fmt.Sprintf("Slave API returned HTTP %d: %s", err.Status, err.Body)
}

func NewSlaveClient() *SlaveClient {
	return &SlaveClient{httpClient: &http.Client{Timeout: 25 * time.Second}}
}

func (client *SlaveClient) List(ctx context.Context, server Server) ([]SlaveRecord, error) {
	var result []SlaveRecord
	if err := client.request(ctx, server, http.MethodGet, "/list", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (client *SlaveClient) Register(ctx context.Context, server Server, email string) (SlaveRecord, error) {
	var result SlaveRecord
	if err := client.request(ctx, server, http.MethodPost, "/register", map[string]string{"email": email}, &result); err != nil {
		return SlaveRecord{}, err
	}
	if result.ID == "" || result.URI == "" || result.Email == "" {
		return SlaveRecord{}, fmt.Errorf("Slave /register returned an incomplete record")
	}
	return result, nil
}

func (client *SlaveClient) Remove(ctx context.Context, server Server, id string) (bool, error) {
	var result struct {
		Removed bool `json:"removed"`
	}
	if err := client.request(ctx, server, http.MethodPost, "/remove", map[string]string{"id": id}, &result); err != nil {
		return false, err
	}
	return result.Removed, nil
}

func (client *SlaveClient) request(ctx context.Context, server Server, method, path string, input any, output any) error {
	endpoint := "https://" + server.DuckDNSURL + path
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return fmt.Errorf("invalid Slave endpoint: %w", err)
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.SetBasicAuth(server.APIUsername, server.APIPassword)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return fmt.Errorf("read Slave response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if len(message) > 512 {
			message = message[:512]
		}
		return &SlaveAPIError{Status: response.StatusCode, Body: message}
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode Slave response: %w", err)
		}
	}
	return nil
}
