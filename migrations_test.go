package main

import "testing"

// The runner itself needs a DB; what can break silently without one is the
// migration list (duplicate/out-of-order versions would corrupt bookkeeping)
// and the skip logic. Both are pure — tested here.

func TestMigrationsWellFormed(t *testing.T) {
	prev := 0
	for _, m := range migrations {
		if m.version <= prev {
			t.Errorf("migration %d (%s): versions must be strictly increasing (prev %d)", m.version, m.name, prev)
		}
		if m.name == "" {
			t.Errorf("migration %d: empty name", m.version)
		}
		if len(m.stmts) == 0 {
			t.Errorf("migration %d (%s): no statements", m.version, m.name)
		}
		prev = m.version
	}
}

func TestPendingMigrationsSkipsApplied(t *testing.T) {
	if len(migrations) < 2 {
		t.Skip("needs at least 2 migrations")
	}
	first := migrations[0].version
	pending := pendingMigrations(map[int]bool{first: true})
	if len(pending) != len(migrations)-1 {
		t.Fatalf("expected %d pending, got %d", len(migrations)-1, len(pending))
	}
	for _, m := range pending {
		if m.version == first {
			t.Fatalf("applied migration %d still pending", first)
		}
	}
	if all := pendingMigrations(nil); len(all) != len(migrations) {
		t.Fatalf("nothing applied: expected all %d pending, got %d", len(migrations), len(all))
	}
}
