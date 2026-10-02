package reservation

// SetAssetStatus changes an asset's status. Always allowed, never rejected
// because of reservations. Cost is O(depth) when the blocking-ness flips.
func (m *Manager) SetAssetStatus(id AssetID, status AssetStatus) error {
	a := m.asset(id)
	if a == nil {
		return ErrNotFound
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := AssetStatus(a.status.Load())
	if old == status {
		return nil
	}
	a.status.Store(int32(status))
	var delta int64
	switch {
	case !blocking(old) && blocking(status):
		delta = 1
	case blocking(old) && !blocking(status):
		delta = -1
	}
	if delta != 0 {
		path := m.lockPath(a.zone.Load())
		for _, n := range path {
			n.state.Add(delta)
		}
		unlockPath(path)
	}
	return nil
}

// MoveAsset moves an asset to another zone. Always allowed.
// A blocking asset is added to the new ancestor chain before it is removed
// from the old one, so any common ancestor (e.g. the building) keeps a
// positive blocked count throughout the move.
func (m *Manager) MoveAsset(id AssetID, zoneID ZoneID) error {
	a := m.asset(id)
	if a == nil {
		return ErrNotFound
	}
	dst := m.zone(zoneID)
	if dst == nil {
		return ErrNotFound
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.zone.Load()
	if src == dst {
		return nil
	}
	if blocking(AssetStatus(a.status.Load())) {
		path := m.lockPath(dst)
		for _, n := range path {
			n.state.Add(1)
		}
		unlockPath(path)
		path = m.lockPath(src)
		for _, n := range path {
			n.state.Add(-1)
		}
		unlockPath(path)
	}
	a.zone.Store(dst)
	return nil
}
