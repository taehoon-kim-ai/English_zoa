package main

import "testing"

func TestChooseQuestionTypeBelowThreshold(t *testing.T) {
	for total := 0; total < minPhrasesForMultipleChoice; total++ {
		if got := chooseQuestionType(trackPhrase, 5, total); got != "word_order" {
			t.Errorf("chooseQuestionType(phrase, 5, %d) = %q, want word_order (not enough phrases for distractors)", total, got)
		}
	}
}

func TestChooseQuestionTypeVocabTrackIsAlwaysMultipleChoice(t *testing.T) {
	// The vocab track only ever holds single words/terms — no meaningful
	// word-order puzzle regardless of word count or pool size.
	for total := 0; total < minPhrasesForMultipleChoice+3; total++ {
		if got := chooseQuestionType(trackVocab, 1, total); got != "multiple_choice" {
			t.Errorf("chooseQuestionType(vocab, 1, %d) = %q, want multiple_choice", total, got)
		}
	}
}

func TestChooseQuestionTypeShortPoolFallsBackToWordOrder(t *testing.T) {
	// Not enough phrases for distractors, but the expression has enough words
	// to arrange — should still produce a playable question.
	if got := chooseQuestionType(trackPhrase, 5, 1); got != "word_order" {
		t.Errorf("chooseQuestionType(phrase, 5, 1) = %q, want word_order", got)
	}
}

func TestValidTrackCount(t *testing.T) {
	for _, c := range vocabCounts {
		if !validTrackCount(trackVocab, c) {
			t.Errorf("validTrackCount(vocab, %d) = false, want true", c)
		}
	}
	for _, c := range phraseCounts {
		if !validTrackCount(trackPhrase, c) {
			t.Errorf("validTrackCount(phrase, %d) = false, want true", c)
		}
	}
	if validTrackCount(trackVocab, 5) {
		t.Errorf("validTrackCount(vocab, 5) = true, want false (5 is a phrase-track count)")
	}
	if validTrackCount(trackPhrase, 30) {
		t.Errorf("validTrackCount(phrase, 30) = true, want false (30 is a vocab-track count)")
	}
	if validTrackCount("bogus", 10) {
		t.Errorf("validTrackCount(bogus, 10) = true, want false")
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
