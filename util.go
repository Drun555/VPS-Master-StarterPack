package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/mail"
	"strings"
)

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validEmail(value string) bool {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func parseSSHAddress(raw string) (host string, port int, normalized string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, "", fmt.Errorf("server address is required")
	}
	port = 22
	if parsedHost, parsedPort, splitErr := net.SplitHostPort(raw); splitErr == nil {
		host = parsedHost
		var parsed int
		if _, scanErr := fmt.Sscanf(parsedPort, "%d", &parsed); scanErr != nil || parsed < 1 || parsed > 65535 {
			return "", 0, "", fmt.Errorf("invalid SSH port")
		}
		port = parsed
	} else {
		if strings.Count(raw, ":") > 1 && !strings.HasPrefix(raw, "[") {
			host = raw
		} else {
			host = strings.Trim(raw, "[]")
		}
	}
	if strings.TrimSpace(host) == "" || strings.ContainsAny(host, " /@") {
		return "", 0, "", fmt.Errorf("invalid server address")
	}
	normalized = net.JoinHostPort(host, fmt.Sprintf("%d", port))
	return host, port, normalized, nil
}

func normalizeDuckDNS(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if !strings.HasSuffix(value, ".duckdns.org") || strings.ContainsAny(value, "/:@ ") || len(value) <= len(".duckdns.org") {
		return "", fmt.Errorf("DuckDNS URL must be a *.duckdns.org hostname")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("DuckDNS URL contains an invalid hostname label")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("DuckDNS URL contains an invalid hostname label")
			}
		}
	}
	return value, nil
}

func findServer(state *State, id string) (*Server, int) {
	for index := range state.Servers {
		if state.Servers[index].ID == id {
			return &state.Servers[index], index
		}
	}
	return nil, -1
}

func findUser(state *State, id string) (*User, int) {
	for index := range state.Users {
		if state.Users[index].ID == id {
			return &state.Users[index], index
		}
	}
	return nil, -1
}
