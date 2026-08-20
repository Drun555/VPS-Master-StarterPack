package main

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFrom(t *testing.T) {
	temporary := t.TempDir()
	path := filepath.Join(temporary, ".env")
	content := "# comment\nPORT=43123\nBASE_URL='https://example.org/root/'\nCERTBOT_EMAIL=admin@example.org\nLISTEN_ADDRESS=127.0.0.1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfigFrom(path, temporary)
	if err != nil {
		t.Fatal(err)
	}
	if config.Port != 43123 || config.BaseURL != "https://example.org/root" || config.ListenAddress != "127.0.0.1" {
		t.Fatalf("unexpected config: %+v", config)
	}
	if config.SlaveSetupURL != defaultSlaveSetupURL {
		t.Fatalf("unexpected default setup URL: %s", config.SlaveSetupURL)
	}
	if config.SlaveUninstallURL != "https://raw.githubusercontent.com/Drun555/VPS-Slave-StarterPack/main/uninstall.sh" {
		t.Fatalf("unexpected uninstall URL: %s", config.SlaveUninstallURL)
	}
}

func TestSiblingScriptURLPreservesAccessQuery(t *testing.T) {
	parsed, err := url.Parse("https://example.org/custom/setup.sh?token=secret#ignored")
	if err != nil {
		t.Fatal(err)
	}
	if got := siblingScriptURL(parsed, "uninstall.sh"); got != "https://example.org/custom/uninstall.sh?token=secret" {
		t.Fatalf("unexpected sibling URL: %s", got)
	}
}

func TestLoadConfigRejectsUnsafeBaseURL(t *testing.T) {
	temporary := t.TempDir()
	path := filepath.Join(temporary, ".env")
	if err := os.WriteFile(path, []byte("BASE_URL=https://user:pass@example.org?x=1\nCERTBOT_EMAIL=a@example.org\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfigFrom(path, temporary); err == nil {
		t.Fatal("expected invalid BASE_URL error")
	}
}

func TestParseSSHAddress(t *testing.T) {
	tests := map[string]string{
		"192.0.2.1":        "192.0.2.1:22",
		"example.org:2200": "example.org:2200",
		"2001:db8::1":      "[2001:db8::1]:22",
	}
	for input, expected := range tests {
		_, _, normalized, err := parseSSHAddress(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if normalized != expected {
			t.Fatalf("%s: got %s, want %s", input, normalized, expected)
		}
	}
}
