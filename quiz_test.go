package main

import "testing"

func TestMinPhrasesForQuizMatchesOptionCount(t *testing.T) {
	// pickQuizQuestion always builds 1 correct + 3 distractors — the minimum
	// pool size must be able to supply that without repeats.
	if minPhrasesForQuiz < 4 {
		t.Fatalf("minPhrasesForQuiz = %d, need at least 4 to fill 1 correct + 3 distractor options", minPhrasesForQuiz)
	}
}
