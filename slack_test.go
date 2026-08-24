package main

import "testing"

func TestParsePhraseTextLabeled(t *testing.T) {
	en, ko, ok := parsePhraseText("EN: Break a leg.\nKR: 행운을 빌어요.")
	if !ok || en != "Break a leg." || ko != "행운을 빌어요." {
		t.Fatalf("got en=%q ko=%q ok=%v", en, ko, ok)
	}
}

func TestParsePhraseTextLabeledReversedOrder(t *testing.T) {
	en, ko, ok := parsePhraseText("한국어: 행운을 빌어요.\nEnglish: Break a leg.")
	if !ok || en != "Break a leg." || ko != "행운을 빌어요." {
		t.Fatalf("got en=%q ko=%q ok=%v", en, ko, ok)
	}
}

func TestParsePhraseTextTwoPlainLines(t *testing.T) {
	en, ko, ok := parsePhraseText("Break a leg.\n행운을 빌어요.")
	if !ok || en != "Break a leg." || ko != "행운을 빌어요." {
		t.Fatalf("got en=%q ko=%q ok=%v", en, ko, ok)
	}
}

func TestParsePhraseTextParenTail(t *testing.T) {
	en, ko, ok := parsePhraseText("Break a leg. (행운을 빌어요.)")
	if !ok || en != "Break a leg." || ko != "행운을 빌어요." {
		t.Fatalf("got en=%q ko=%q ok=%v", en, ko, ok)
	}
}

func TestParsePhraseTextUnparseable(t *testing.T) {
	if _, _, ok := parsePhraseText("that's all for now, thanks everyone"); ok {
		t.Fatalf("expected unparseable single line to fail")
	}
}

func TestParsePhraseTextStripsSlackMarkup(t *testing.T) {
	en, ko, ok := parsePhraseText("<@U123> EN: Break a leg.\nKR: 행운을 빌어요. <https://example.com>")
	if !ok || en != "Break a leg." || ko != "행운을 빌어요." {
		t.Fatalf("got en=%q ko=%q ok=%v", en, ko, ok)
	}
}
