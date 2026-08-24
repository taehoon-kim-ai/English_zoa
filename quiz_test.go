package main

import "testing"

func TestChooseQuestionTypeBelowThreshold(t *testing.T) {
	for total := 0; total < minPhrasesForMultipleChoice; total++ {
		if got := chooseQuestionType(total); got != "word_order" {
			t.Errorf("chooseQuestionType(%d) = %q, want word_order (not enough phrases for distractors)", total, got)
		}
	}
}

func TestBuildWordOrderChipsRoundTrips(t *testing.T) {
	english := "Let's circle back on this next week."
	options, order := buildWordOrderChips(english)

	if len(options) != 7 {
		t.Fatalf("expected 7 word chips, got %d", len(options))
	}
	if len(order) != len(options) {
		t.Fatalf("order length %d != options length %d", len(order), len(options))
	}

	// Reassembling the chips in `order` must reproduce the original sentence.
	byID := map[string]string{}
	for _, opt := range options {
		byID[opt.ID] = opt.Text
	}
	words := make([]string, len(order))
	for i, id := range order {
		text, ok := byID[id]
		if !ok {
			t.Fatalf("order id %q has no matching chip", id)
		}
		words[i] = text
	}
	got := joinWords(words)
	if got != english {
		t.Fatalf("reassembled %q, want %q", got, english)
	}

	// ids must be unique (duplicate words in a sentence still get distinct ids).
	seen := map[string]bool{}
	for _, opt := range options {
		if seen[opt.ID] {
			t.Fatalf("duplicate chip id %q", opt.ID)
		}
		seen[opt.ID] = true
	}
}

func TestSlicesEqual(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, true},
		{[]string{"a", "b", "c"}, []string{"a", "c", "b"}, false},
		{[]string{"a", "b"}, []string{"a", "b", "c"}, false},
		{nil, nil, true},
	}
	for _, tc := range cases {
		if got := slicesEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("slicesEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func joinWords(words []string) string {
	out := ""
	for i, w := range words {
		if i > 0 {
			out += " "
		}
		out += w
	}
	return out
}
