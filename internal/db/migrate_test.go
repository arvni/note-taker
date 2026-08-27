package db

import "testing"

func TestLoadMigrationsPairsAndOrders(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("expected at least one migration")
	}
	// Versions must be ascending and every migration must have both directions.
	var prev int64
	for i, m := range migs {
		if m.up == "" || m.down == "" {
			t.Errorf("migration %d (%s) missing up or down SQL", m.version, m.name)
		}
		if i > 0 && m.version <= prev {
			t.Errorf("migrations not strictly ascending: %d after %d", m.version, prev)
		}
		prev = m.version
	}
	// The known first migration must be present and paired.
	if migs[0].version != 1 || migs[0].name != "core_tables" {
		t.Errorf("first migration = %d_%s, want 1_core_tables", migs[0].version, migs[0].name)
	}
}
