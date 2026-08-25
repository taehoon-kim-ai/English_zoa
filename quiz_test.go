package main

import "testing"

func TestChooseQuestionTypeBelowThreshold(t *testing.T) {
	for total := 0; total < minPhrasesForMultipleChoice; total++ {
		if got := chooseQuestionType(trackPhrase, "medium", 5, total); got != "word_order" {
			t.Errorf("chooseQuestionType(phrase, 5, %d) = %q, want word_order (not enough phrases for distractors)", total, got)
		}
	}
}

func TestChooseQuestionTypeVocabTrackIsAlwaysMultipleChoice(t *testing.T) {
	// The vocab track only ever holds single words/terms — no meaningful
	// word-order puzzle regardless of word count or pool size.
	for total := 0; total < minPhrasesForMultipleChoice+3; total++ {
		if got := chooseQuestionType(trackVocab, "medium", 1, total); got != "multiple_choice" {
			t.Errorf("chooseQuestionType(vocab, 1, %d) = %q, want multiple_choice", total, got)
		}
	}
}

func TestChooseQuestionTypeShortPoolFallsBackToWordOrder(t *testing.T) {
	// Not enough phrases for distractors, but the expression has enough words
	// to arrange — should still produce a playable question.
	if got := chooseQuestionType(trackPhrase, "medium", 5, 1); got != "word_order" {
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

// The duplicate-word case that motivated word-based grading: "the" appears
// twice, so chips b and d render identically. A submission that swaps them
// spells the exact same sentence and must be correct.
func TestWordOrderMatchesDuplicateWords(t *testing.T) {
	options := []QuizOption{
		{ID: "a", Text: "The"},
		{ID: "b", Text: "more"},
		{ID: "c", Text: "the"},
		{ID: "d", Text: "better."},
	}
	correct := []string{"a", "b", "c", "d"}

	if !wordOrderMatches(options, []string{"a", "b", "c", "d"}, correct) {
		t.Errorf("exact id order should match")
	}

	// Sentence with a true duplicate: "step by step by step" style.
	dupOptions := []QuizOption{
		{ID: "w1", Text: "step"},
		{ID: "w2", Text: "by"},
		{ID: "w3", Text: "step"},
	}
	dupCorrect := []string{"w1", "w2", "w3"}
	// User tapped the visually-identical "step" chips in the other order.
	if !wordOrderMatches(dupOptions, []string{"w3", "w2", "w1"}, dupCorrect) {
		t.Errorf("swapping identical-word chips must still be correct")
	}
	// A genuinely wrong order must still fail.
	if wordOrderMatches(dupOptions, []string{"w2", "w1", "w3"}, dupCorrect) {
		t.Errorf("wrong word order must fail")
	}
}

func TestWordOrderMatchesRejectsBadInput(t *testing.T) {
	options := []QuizOption{{ID: "a", Text: "hi"}, {ID: "b", Text: "there"}}
	correct := []string{"a", "b"}
	if wordOrderMatches(options, nil, correct) {
		t.Errorf("empty submission must fail")
	}
	if wordOrderMatches(options, []string{"a"}, correct) {
		t.Errorf("short submission must fail")
	}
	if wordOrderMatches(options, []string{"a", "zzz"}, correct) {
		t.Errorf("unknown chip id must fail")
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

func TestChooseQuestionTypeDifficulty(t *testing.T) {
	// Easy phrase questions are always multiple-choice when the pool allows;
	// hard ones are always word-order when the sentence has enough words.
	if got := chooseQuestionType(trackPhrase, "easy", 6, 20); got != "multiple_choice" {
		t.Errorf("easy phrase = %q, want multiple_choice", got)
	}
	if got := chooseQuestionType(trackPhrase, "hard", 6, 20); got != "word_order" {
		t.Errorf("hard phrase = %q, want word_order", got)
	}
	// Vocab stays multiple-choice at every difficulty.
	if got := chooseQuestionType(trackVocab, "hard", 1, 20); got != "multiple_choice" {
		t.Errorf("hard vocab = %q, want multiple_choice", got)
	}
}

func TestDifficultyOptionCount(t *testing.T) {
	if difficultyOptionCount("easy") != 3 || difficultyOptionCount("medium") != 4 || difficultyOptionCount("hard") != 6 {
		t.Errorf("unexpected option counts: %d/%d/%d",
			difficultyOptionCount("easy"), difficultyOptionCount("medium"), difficultyOptionCount("hard"))
	}
}
