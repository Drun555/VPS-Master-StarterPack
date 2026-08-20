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
