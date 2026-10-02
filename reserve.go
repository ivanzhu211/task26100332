package facility

// Reserve reserves a zone and returns a reservation ID.
//
// Protocol (all per-zone fields are atomics; no locks are taken on
// ancestors, so concurrent operations on unrelated zones never wait on
// each other):
//
//  1. Reject if the subtree holds blocking assets or reservations.
//  2. CAS own state free->pending, then re-read subRes. This pairs with
//     step 3 of a concurrent Reserve on a descendant (its subRes add vs.
//     its read of our state): with sequentially consistent atomics at
//     least one side observes the other, so two ancestor/descendant
//     reservations can never both confirm.
//  3. Walk up the ancestor chain, adding +1 to each ancestor's subRes
//     and then checking the ancestor's state. On conflict, roll back.
//
// A concurrent structural change (MoveZone) is excluded by the read lock,
// so the ancestor chain is stable for the whole operation.
func (m *Manager) Reserve(zoneID string) (string, error) {
	m.structMu.RLock()
	defer m.structMu.RUnlock()

	z, err := m.lookupZone(zoneID)
	if err != nil {
		return "", err
	}
	if z.subBlock.Load() != 0 || z.subRes.Load() != 0 {
		return "", ErrRejected
	}
	if !z.state.CompareAndSwap(resFree, resPending) {
		return "", ErrRejected
	}
	if z.subRes.Load() != 0 {
		z.state.Store(resFree)
		return "", ErrRejected
	}
	z.subRes.Add(1)

	// ancestors of z, bottom-up
	var ancestors []*zone
	for cur := z.parent; cur != nil; cur = cur.parent {
		ancestors = append(ancestors, cur)
	}
	applied := 0
	for _, a := range ancestors {
		a.subRes.Add(1)
		applied++
		if a.state.Load() != resFree {
			for i := 0; i < applied; i++ {
				ancestors[i].subRes.Add(-1)
			}
			z.subRes.Add(-1)
			z.state.Store(resFree)
			return "", ErrRejected
		}
	}

	z.state.Store(resConfirmed)
	id := m.newReservationID()
	m.resvs.Store(id, &reservation{id: id, zone: z})
	return id, nil
}

// Release releases a reservation.
func (m *Manager) Release(reservationID string) error {
	v, ok := m.resvs.LoadAndDelete(reservationID)
	if !ok {
		return ErrNotFound
	}
	r := v.(*reservation)

	m.structMu.RLock()
	defer m.structMu.RUnlock()

	z := r.zone
	z.state.Store(resFree)
	addReserved(z, -1)
	return nil
}

// IsReservable reports whether the zone is currently reservable:
// no blocking asset in its subtree, and no active reservation on the
// zone itself, any ancestor, or any descendant.
func (m *Manager) IsReservable(zoneID string) (bool, error) {
	m.structMu.RLock()
	defer m.structMu.RUnlock()

	z, err := m.lookupZone(zoneID)
	if err != nil {
		return false, err
	}
	if z.subBlock.Load() != 0 || z.subRes.Load() != 0 {
		return false, nil
	}
	for cur := z.parent; cur != nil; cur = cur.parent {
		if cur.state.Load() != resFree {
			return false, nil
		}
	}
	return true, nil
}
