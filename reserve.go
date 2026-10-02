package reservation

// Reserve tries to reserve a zone. It succeeds iff at the moment it takes
// effect: the zone's subtree has no blocking asset, no ancestor (incl. the
// zone itself) has an active reservation, and no descendant has one.
// Returns ErrNotReservable on rejection.
func (m *Manager) Reserve(zoneID ZoneID) (ResID, error) {
	z := m.zone(zoneID)
	if z == nil {
		return 0, ErrNotFound
	}
	path := m.lockPath(z)
	defer unlockPath(path)

	// Claim the zone itself: atomically requires blocked==0, subRes==0
	// and selfRes==0.
	if !z.state.CompareAndSwap(0, selfUnit) {
		return 0, ErrNotReservable
	}
	// Walk ancestors nearest-first: each must have no reservation of its
	// own; bump their descendant-reservation count as we go.
	var done []*Zone
	for i := len(path) - 2; i >= 0; i-- {
		a := path[i]
		ok := false
		for {
			s := a.state.Load()
			if s&selfUnit != 0 {
				break
			}
			if a.state.CompareAndSwap(s, s+resUnit) {
				ok = true
				break
			}
		}
		if !ok {
			for _, d := range done {
				d.state.Add(-resUnit)
			}
			z.state.Add(-selfUnit)
			return 0, ErrNotReservable
		}
		done = append(done, a)
	}
	id := ResID(m.nextRes.Add(1))
	m.reservations.Store(id, &Reservation{ID: id, Zone: z})
	return id, nil
}

// Release releases an active reservation.
func (m *Manager) Release(id ResID) error {
	v, ok := m.reservations.LoadAndDelete(id)
	if !ok {
		return ErrNotFound
	}
	r := v.(*Reservation)
	path := m.lockPath(r.Zone)
	defer unlockPath(path)
	r.Zone.state.Add(-selfUnit)
	for i := len(path) - 2; i >= 0; i-- {
		path[i].state.Add(-resUnit)
	}
	return nil
}

// IsReservable reports whether the zone currently satisfies all three
// reservability conditions. O(depth).
func (m *Manager) IsReservable(zoneID ZoneID) bool {
	z := m.zone(zoneID)
	if z == nil {
		return false
	}
	path := m.lockPath(z)
	defer unlockPath(path)
	if z.state.Load() != 0 {
		return false
	}
	for i := len(path) - 2; i >= 0; i-- {
		if path[i].state.Load()&selfUnit != 0 {
			return false
		}
	}
	return true
}
