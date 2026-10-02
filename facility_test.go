package facility

import "testing"

func mustAddZone(t *testing.T, m *Manager, id, parent string) {
	t.Helper()
	if err := m.AddZone(id, parent); err != nil {
		t.Fatalf("AddZone(%s,%s): %v", id, parent, err)
	}
}

func mustReservable(t *testing.T, m *Manager, id string, want bool) {
	t.Helper()
	got, err := m.IsReservable(id)
	if err != nil {
		t.Fatalf("IsReservable(%s): %v", id, err)
	}
	if got != want {
		t.Fatalf("IsReservable(%s) = %v, want %v", id, got, want)
	}
}

// Tree: F -> B -> L1 -> R1; asset A in R1.
func buildSimple(t *testing.T) *Manager {
	t.Helper()
	m := NewManager()
	mustAddZone(t, m, "F", "")
	mustAddZone(t, m, "B", "F")
	mustAddZone(t, m, "L1", "B")
	mustAddZone(t, m, "R1", "L1")
	if err := m.AddAsset("A", "R1"); err != nil {
		t.Fatalf("AddAsset: %v", err)
	}
	return m
}

func TestBlockingAssetBlocksAncestors(t *testing.T) {
	m := buildSimple(t)
	for _, z := range []string{"R1", "L1", "B", "F"} {
		mustReservable(t, m, z, true)
	}
	if err := m.SetAssetStatus("A", Maintenance); err != nil {
		t.Fatal(err)
	}
	for _, z := range []string{"R1", "L1", "B", "F"} {
		mustReservable(t, m, z, false)
	}
	if err := m.SetAssetStatus("A", Normal); err != nil {
		t.Fatal(err)
	}
	mustReservable(t, m, "F", true)
}

func TestMoveAssetRecoversReservability(t *testing.T) {
	m := buildSimple(t)
	mustAddZone(t, m, "F2", "")
	if err := m.SetAssetStatus("A", Decommissioned); err != nil {
		t.Fatal(err)
	}
	mustReservable(t, m, "F", false)
	if err := m.MoveAsset("A", "F2"); err != nil {
		t.Fatal(err)
	}
	mustReservable(t, m, "F", true)
	mustReservable(t, m, "F2", false)
}

func TestReserveReleaseAndNesting(t *testing.T) {
	m := buildSimple(t)
	if _, err := m.Reserve("B"); err != nil {
		t.Fatalf("Reserve(B): %v", err)
	}
	// Ancestor, self and descendant all blocked now.
	for _, z := range []string{"F", "B", "L1", "R1"} {
		mustReservable(t, m, z, false)
		if _, err := m.Reserve(z); err == nil {
			t.Fatalf("Reserve(%s) should fail while B is reserved", z)
		}
	}
	// A subtree outside B's branch is unaffected.
	mustAddZone(t, m, "B2", "F")
	mustAddZone(t, m, "L2", "B2")
	mustReservable(t, m, "L2", true)
}

func TestReleaseRestores(t *testing.T) {
	m := buildSimple(t)
	id, err := m.Reserve("L1")
	if err != nil {
		t.Fatal(err)
	}
	mustReservable(t, m, "B", false)
	if err := m.Release(id); err != nil {
		t.Fatal(err)
	}
	mustReservable(t, m, "B", true)
	mustReservable(t, m, "L1", true)
	if err := m.Release(id); err == nil {
		t.Fatal("double release should fail")
	}
}

func TestMoveZoneBasics(t *testing.T) {
	m := buildSimple(t)
	mustAddZone(t, m, "B2", "F")
	if err := m.SetAssetStatus("A", Maintenance); err != nil {
		t.Fatal(err)
	}
	if err := m.MoveZone("L1", "B2"); err != nil {
		t.Fatalf("MoveZone: %v", err)
	}
	mustReservable(t, m, "B", true)
	mustReservable(t, m, "B2", false)
	mustReservable(t, m, "F", false)

	// Cycle attempts.
	if err := m.MoveZone("B2", "L1"); err != ErrCycle {
		t.Fatalf("want ErrCycle, got %v", err)
	}
	if err := m.MoveZone("L1", "L1"); err != ErrCycle {
		t.Fatalf("want ErrCycle, got %v", err)
	}
	if err := m.MoveZone("F", "B"); err != ErrZoneIsRoot {
		t.Fatalf("want ErrZoneIsRoot, got %v", err)
	}
}

func TestMoveZoneRejectsNestedReservations(t *testing.T) {
	m := buildSimple(t)
	mustAddZone(t, m, "B2", "F")
	mustAddZone(t, m, "L2", "B2")
	if _, err := m.Reserve("L1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reserve("L2"); err != nil {
		t.Fatal(err)
	}
	// Moving L1 (reserved) under B2 (has reserved descendant L2 is fine:
	// L2 is not on the new ancestor chain). But moving L1 under L2 must fail.
	if err := m.MoveZone("L1", "L2"); err != ErrReservation {
		t.Fatalf("want ErrReservation, got %v", err)
	}
	if err := m.MoveZone("L1", "B2"); err != nil {
		t.Fatalf("MoveZone(L1,B2) should succeed: %v", err)
	}
}
