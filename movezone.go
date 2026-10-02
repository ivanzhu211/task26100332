package reservation

import "sort"

// MoveZone re-parents a non-root zone (with its subtree) under newParent.
// It is rejected if newParent is the zone itself or one of its descendants
// (would create a cycle), or if the move would place an active reservation
// below another active reservation. Cost is O(depth), independent of the
// moved subtree's size.
func (m *Manager) MoveZone(id, newParentID ZoneID) error {
	m.moveMu.Lock()
	defer m.moveMu.Unlock()

	z := m.zone(id)
	np := m.zone(newParentID)
	if z == nil || np == nil {
		return ErrNotFound
	}
	oldP := z.parent.Load()
	if oldP == nil {
		return ErrRootMove
	}
	if oldP == np {
		return nil
	}

	// Ancestry is stable while moveMu is held, so chains can be recorded
	// lock-free. Walking up from newParent must not pass through z.
	newChain := chainToRoot(np) // root .. newParent
	for _, n := range newChain {
		if n == z {
			return ErrCycle
		}
	}
	oldChain := chainToRoot(oldP) // root .. oldParent

	// Write-lock the union of both chains in a global (depth, id) order;
	// walking readers lock top-down in the same order, so no deadlock.
	type item struct {
		z     *Zone
		depth int
	}
	seen := make(map[*Zone]bool)
	var locks []item
	add := func(chain []*Zone) {
		for i, n := range chain {
			if !seen[n] {
				seen[n] = true
				locks = append(locks, item{n, i})
			}
		}
	}
	add(oldChain)
	add(newChain)
	sort.Slice(locks, func(i, j int) bool {
		if locks[i].depth != locks[j].depth {
			return locks[i].depth < locks[j].depth
		}
		return locks[i].z.id < locks[j].z.id
	})
	for _, it := range locks {
		it.z.mu.Lock()
	}
	defer func() {
		for _, it := range locks {
			it.z.mu.Unlock()
		}
	}()

	// Both chains are now quiescent: no walking op can touch them or
	// anything inside the moved subtree.
	s := z.state.Load()
	delta := blockedOf(s) + subResOf(s)*resUnit
	if s&selfUnit != 0 {
		delta += resUnit
	}
	// Reject if the move would nest a reservation inside the moved subtree
	// below a reservation on the new ancestor chain.
	if subResOf(s)+selfOf(s) > 0 {
		for _, n := range newChain {
			if n.state.Load()&selfUnit != 0 {
				return ErrReservationConflict
			}
		}
	}
	// Add to the new chain before subtracting from the old one, so shared
	// ancestors never observe a zeroed-out blocked count mid-move.
	for _, n := range newChain {
		n.state.Add(delta)
	}
	for _, n := range oldChain {
		n.state.Add(-delta)
	}
	z.parent.Store(np)
	oldP.cMu.Lock()
	delete(oldP.children, z)
	oldP.cMu.Unlock()
	np.cMu.Lock()
	np.children[z] = struct{}{}
	np.cMu.Unlock()
	return nil
}
