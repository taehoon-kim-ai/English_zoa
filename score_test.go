package main

import (
	"testing"
	"time"
)

func TestWeekStart(t *testing.T) {
	mustParse := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, seoulTZ)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return tm
	}
	cases := []struct{ now, want string }{
		// 2026-08-21 is a Friday.
		{"2026-08-25 14:00", "2026-08-21 09:00"}, // Tuesday mid-week
		{"2026-08-21 09:00", "2026-08-21 09:00"}, // Friday exactly 09:00 → new week starts
		{"2026-08-21 10:30", "2026-08-21 09:00"}, // Friday after 09:00
		{"2026-08-21 08:59", "2026-08-14 09:00"}, // Friday before 09:00 → still last week
		{"2026-08-28 08:00", "2026-08-21 09:00"}, // next Thursday night equivalent
		{"2026-08-28 09:00", "2026-08-28 09:00"}, // next Friday 09:00 rolls over
	}
	for _, tc := range cases {
		if got := weekStart(mustParse(tc.now)); !got.Equal(mustParse(tc.want)) {
			t.Errorf("weekStart(%s) = %s, want %s", tc.now, got.Format("2006-01-02 15:04"), tc.want)
		}
	}
}

func TestBattleHint(t *testing.T) {
	if got := battleHint("synergy", 0); got != "" {
		t.Errorf("level 0 = %q, want empty", got)
	}
	if got := battleHint("synergy", 1); got != "s••••••" {
		t.Errorf("level 1 = %q, want s••••••", got)
	}
	if got := battleHint("touch base", 2); got != "to••• ba••" {
		t.Errorf("level 2 = %q, want to••• ba••", got)
	}
	// punctuation always shows; high level fully reveals
	if got := battleHint("circle back.", 99); got != "circle back." {
		t.Errorf("level 99 = %q, want full reveal", got)
	}
}
