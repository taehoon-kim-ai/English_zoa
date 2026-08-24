package main

import "testing"

func TestFallbackPhrasesHaveBothCategories(t *testing.T) {
	hasVocab, hasExpression := false, false
	for _, p := range fallbackPhrases {
		switch p.Category {
		case "vocabulary":
			hasVocab = true
		case "expression":
			hasExpression = true
		default:
			t.Fatalf("fallbackPhrases entry %q has invalid category %q", p.En, p.Category)
		}
	}
	if !hasVocab || !hasExpression {
		t.Fatalf("expected both vocabulary and expression entries, hasVocab=%v hasExpression=%v", hasVocab, hasExpression)
	}
}

func TestFallbackPhrasesAreComplete(t *testing.T) {
	for i, p := range fallbackPhrases {
		if p.En == "" || p.Ko == "" {
			t.Errorf("fallbackPhrases[%d] has an empty field: %+v", i, p)
		}
	}
}

// The phrases table enforces a unique index on english_text (phrase.go
// schema) so bulk seeding is idempotent — the static list itself must not
// contain duplicates, or seeding would silently drop entries.
func TestFallbackPhrasesHaveNoDuplicateEnglish(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range fallbackPhrases {
		if seen[p.En] {
			t.Errorf("duplicate fallbackPhrases entry: %q", p.En)
		}
		seen[p.En] = true
	}
}
