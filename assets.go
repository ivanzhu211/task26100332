package facility

// AddAsset creates an asset in the given zone with status Normal.
func (m *Manager) AddAsset(id, zoneID string) error {
	m.structMu.RLock()
	defer m.structMu.RUnlock()

	if _, exists := m.assets.Load(id); exists {
		return ErrAlreadyExists
	}
	z, err := m.lookupZone(zoneID)
	if err != nil {
		return err
	}
	m.assets.Store(id, &asset{id: id, zone: z, status: Normal})
	return nil
}

// SetAssetStatus changes an asset's status. Always allowed, never
// rejected because of reservations.
//
// Counter propagation happens while holding the asset lock, so all
// counter transitions caused by one asset are serialized: an asset's
// contribution to any chain is always 0 or 1, never negative. For a
// transition into a blocking state the +1 is applied before the status is
// committed; for a transition out of a blocking state the -1 is applied
// after. Counters therefore never undercount committed blocking assets,
// and a concurrent Reserve can only be conservatively rejected.
func (m *Manager) SetAssetStatus(assetID string, status Status) error {
	if status != Normal && status != Maintenance && status != Decommissioned {
		return ErrInvalidStatus
	}
	m.structMu.RLock()
	defer m.structMu.RUnlock()

	a, err := m.lookupAsset(assetID)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	delta := bool2int(status.blocking()) - bool2int(a.status.blocking())
	if delta > 0 {
		addBlocked(a.zone, int64(delta))
	}
	a.status = status
	if delta < 0 {
		addBlocked(a.zone, int64(delta))
	}
	return nil
}

// MoveAsset moves an asset to another zone. Always allowed.
//
// If the asset is blocking, the +1 on the new chain is applied before the
// -1 on the old chain, so any common ancestor (e.g. the building when a
// broken device moves between rooms of that building) never observes a
// zero count, not even for an instant.
func (m *Manager) MoveAsset(assetID, zoneID string) error {
	m.structMu.RLock()
	defer m.structMu.RUnlock()

	a, err := m.lookupAsset(assetID)
	if err != nil {
		return err
	}
	nz, err := m.lookupZone(zoneID)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	old := a.zone
	if old == nz {
		return nil
	}
	if a.status.blocking() {
		addBlocked(nz, 1)
	}
	a.zone = nz
	if a.status.blocking() {
		addBlocked(old, -1)
	}
	return nil
}

func bool2int(b bool) int {
	if b {
		return 1
	}
	return 0
}
