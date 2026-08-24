package main

import "testing"

func TestDefaultNickname(t *testing.T) {
	cases := map[string]string{
		"taehoon.kim@applied.co":   "taehoon.kim",
		"seungheon.lee@applied.co": "seungheon.lee",
		"":                         "익명",
	}
	for email, want := range cases {
		if got := defaultNickname(email); got != want {
			t.Errorf("defaultNickname(%q) = %q, want %q", email, got, want)
		}
	}
}
