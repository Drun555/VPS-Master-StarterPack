package main

import "testing"

func TestValidProfileName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "plain name", value: "Иван", want: true},
		{name: "spaces inside", value: "Домашний профиль 1", want: true},
		{name: "not an email", value: "router-user", want: true},
		{name: "trimmed empty", value: "   ", want: false},
		{name: "too long", value: string(make([]byte, 255)), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validProfileName(test.value); got != test.want {
				t.Fatalf("validProfileName(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestNormalizeServerDisplayName(t *testing.T) {
	if got, err := normalizeServerDisplayName("  Нидерланды  "); err != nil || got != "Нидерланды" {
		t.Fatalf("unexpected display name %q: %v", got, err)
	}
	if got, err := normalizeServerDisplayName(""); err != nil || got != "" {
		t.Fatalf("empty display name must reset the override: %q, %v", got, err)
	}
	if _, err := normalizeServerDisplayName("bad\nname"); err == nil {
		t.Fatal("control characters must be rejected")
	}
}
