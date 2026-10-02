package facility

// AddZone creates a zone. parentID == "" creates a facility root.
func (m *Manager) AddZone(id, parentID string) error {
	m.structMu.RLock()
	defer m.structMu.RUnlock()

	if _, exists := m.zones.Load(id); exists {
		return ErrAlreadyExists
	}
	var parent *zone
	if parentID != "" {
		p, err := m.lookupZone(parentID)
		if err != nil {
			return err
		}
		parent = p
	}
	z := &zone{id: id, parent: parent}
	m.zones.Store(id, z)
	return nil
}

// MoveZone moves a non-root zone (with its subtree) under newParentID.
//
// It is the only structural mutation and runs under the write lock, so no
// other operation observes intermediate states. Its cost is O(depth): the
// moved subtree's aggregate counters are re-rooted without walking the
// subtree itself.
func (m *Manager) MoveZone(zoneID, newParentID string) error {
	m.structMu.Lock()
	defer m.structMu.Unlock()

	z, err := m.lookupZone(zoneID)
	if err != nil {
		return err
	}
	if z.parent == nil {
		return ErrZoneIsRoot
	}
	np, err := m.lookupZone(newParentID)
	if err != nil {
		return err
	}
	if np == z {
		return ErrCycle
	}

	// Cycle check: newParent must not lie in z's subtree.
	newChain := chain(np) // np first, up to its root
	for _, a := range newChain {
		if a == z {
			return ErrCycle
		}
	}

	// Reservation check: if the moved subtree holds active reservations,
	// no active reservation may sit on the new ancestor chain (that would
	// nest one active reservation below another).
	if z.subRes.Load() > 0 {
		for _, a := range newChain {
			if a.state.Load() != resFree {
				return ErrReservation
			}
		}
	}

	// Re-root the subtree aggregates: add to the new chain first, then
	// remove from the old chain, so shared ancestors never see a dropped
	// count (a blocking asset in transit must keep its building blocked).
	sb := z.subBlock.Load()
	sr := z.subRes.Load()
	for _, a := range newChain {
		a.subBlock.Add(sb)
		a.subRes.Add(sr)
	}
	oldParent := z.parent
	z.parent = np
	for cur := oldParent; cur != nil; cur = cur.parent {
		cur.subBlock.Add(-sb)
		cur.subRes.Add(-sr)
	}
	return nil
}
