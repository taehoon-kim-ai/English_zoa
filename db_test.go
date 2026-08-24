package main

import (
	"testing"
	"time"
)

func TestDefaultNickname(t *testing.T) {
	cases := map[string]string{
		"taehoon.kim@applied.co": "taehoon.kim",
		"seungheon.lee@applied.co": "seungheon.lee",
		"":                        "익명",
	}
	for email, want := range cases {
		if got := defaultNickname(email); got != want {
			t.Errorf("defaultNickname(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestFallbackPhraseIsDeterministicPerDay(t *testing.T) {
	d := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	en1, ko1 := fallbackPhrase(d)
	en2, ko2 := fallbackPhrase(d)
	if en1 != en2 || ko1 != ko2 {
		t.Fatalf("fallbackPhrase should be deterministic for the same date")
	}
	if en1 == "" || ko1 == "" {
		t.Fatalf("fallbackPhrase returned empty text")
	}
}

func TestFallbackPhraseVariesAcrossDays(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < len(fallbackPhrases); i++ {
		en, _ := fallbackPhrase(time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC))
		seen[en] = true
	}
	if len(seen) != len(fallbackPhrases) {
		t.Fatalf("expected %d distinct phrases across a full cycle, got %d", len(fallbackPhrases), len(seen))
	}
}
