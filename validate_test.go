package reservation

import "testing"

// allZones returns a snapshot of every zone.
func (m *Manager) allZones() []*Zone {
	m.zonesMu.RLock()
	defer m.zonesMu.RUnlock()
	out := make([]*Zone, 0, len(m.zones))
	for _, z := range m.zones {
		out = append(out, z)
	}
	return out
}

// isAncestor reports whether a is a strict ancestor of b.
func isAncestor(a, b *Zone) bool {
	for cur := b.parent.Load(); cur != nil; cur = cur.parent.Load() {
		if cur == a {
			return true
		}
	}
	return false
}

// validate recomputes every counter and every zone's reservability from
// scratch and compares against what the library reports. Call only when
// no operations are in flight.
func validate(t *testing.T, m *Manager) {
	t.Helper()
	zones := m.allZones()

	// 1. Tree structure: parent chains terminate at a root; children sets
	//    agree with parent pointers.
	roots := 0
	for _, z := range zones {
		depth := 0
		for cur := z; cur.parent.Load() != nil; cur = cur.parent.Load() {
			depth++
			if depth > 1000 {
				t.Fatalf("zone %d: parent chain does not terminate (cycle?)", z.id)
			}
		}
		if z.parent.Load() == nil {
			roots++
		}
		z.cMu.Lock()
		for c := range z.children {
			if c.parent.Load() != z {
				t.Fatalf("zone %d lists child %d whose parent is %v", z.id, c.id, c.parent.Load())
			}
		}
		z.cMu.Unlock()
		if p := z.parent.Load(); p != nil {
			p.cMu.Lock()
			_, ok := p.children[z]
			p.cMu.Unlock()
			if !ok {
				t.Fatalf("zone %d's parent %d does not list it as child", z.id, p.id)
			}
		}
	}
	if roots == 0 {
		t.Fatal("no facility root found")
	}

	// 2. Recompute counters from scratch.
	expBlocked := make(map[*Zone]int64)
	m.assetsMu.RLock()
	for _, a := range m.assets {
		if !blocking(AssetStatus(a.status.Load())) {
			continue
		}
		for cur := a.zone.Load(); cur != nil; cur = cur.parent.Load() {
			expBlocked[cur]++
		}
	}
	m.assetsMu.RUnlock()

	expSub := make(map[*Zone]int64)
	expSelf := make(map[*Zone]int64)
	m.reservations.Range(func(_, v any) bool {
		r := v.(*Reservation)
		expSelf[r.Zone]++
		for cur := r.Zone.parent.Load(); cur != nil; cur = cur.parent.Load() {
			expSub[cur]++
		}
		return true
	})

	for _, z := range zones {
		s := z.state.Load()
		if got, want := blockedOf(s), expBlocked[z]; got != want {
			t.Fatalf("zone %d: blocked=%d, want %d", z.id, got, want)
		}
		if got, want := subResOf(s), expSub[z]; got != want {
			t.Fatalf("zone %d: subRes=%d, want %d", z.id, got, want)
		}
		if got, want := selfOf(s), expSelf[z]; got != want {
			t.Fatalf("zone %d: selfRes=%d, want %d", z.id, got, want)
		}
	}

	// 3. Reservability by definition vs IsReservable, and Reserve agrees.
	reservable := make(map[*Zone]bool)
	for _, z := range zones {
		ok := expBlocked[z] == 0 && expSelf[z] == 0 && expSub[z] == 0
		for cur := z.parent.Load(); cur != nil && ok; cur = cur.parent.Load() {
			if expSelf[cur] > 0 {
				ok = false
			}
		}
		reservable[z] = ok
		if got := m.IsReservable(z.id); got != ok {
			t.Fatalf("zone %d: IsReservable=%v, want %v", z.id, got, ok)
		}
	}
	for _, z := range zones {
		id, err := m.Reserve(z.id)
		if reservable[z] {
			if err != nil {
				t.Fatalf("zone %d: Reserve rejected but definition says reservable", z.id)
			}
			if err := m.Release(id); err != nil {
				t.Fatalf("zone %d: Release: %v", z.id, err)
			}
		} else if err == nil {
			m.Release(id)
			t.Fatalf("zone %d: Reserve succeeded but definition says not reservable", z.id)
		}
	}
}
