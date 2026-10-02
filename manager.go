package reservation

import (
	"sync"
	"sync/atomic"
)

// Manager keeps the whole facility forest in memory.
type Manager struct {
	zonesMu sync.RWMutex
	zones   map[ZoneID]*Zone

	assetsMu sync.RWMutex
	assets   map[AssetID]*Asset

	reservations sync.Map // ResID -> *Reservation

	nextZone  atomic.Int64
	nextAsset atomic.Int64
	nextRes   atomic.Int64

	moveMu sync.Mutex // serializes MoveZone
}

func NewManager() *Manager {
	return &Manager{
		zones:  make(map[ZoneID]*Zone),
		assets: make(map[AssetID]*Asset),
	}
}

func (m *Manager) zone(id ZoneID) *Zone {
	m.zonesMu.RLock()
	defer m.zonesMu.RUnlock()
	return m.zones[id]
}

func (m *Manager) asset(id AssetID) *Asset {
	m.assetsMu.RLock()
	defer m.assetsMu.RUnlock()
	return m.assets[id]
}

func (m *Manager) addZone(z *Zone) ZoneID {
	id := ZoneID(m.nextZone.Add(1))
	z.id = id
	m.zonesMu.Lock()
	m.zones[id] = z
	m.zonesMu.Unlock()
	return id
}

// AddFacility creates a root zone (no parent).
func (m *Manager) AddFacility() ZoneID {
	return m.addZone(&Zone{children: make(map[*Zone]struct{})})
}

// AddZone creates a zone under parent.
func (m *Manager) AddZone(parent ZoneID) (ZoneID, error) {
	p := m.zone(parent)
	if p == nil {
		return 0, ErrNotFound
	}
	z := &Zone{children: make(map[*Zone]struct{})}
	z.parent.Store(p)
	id := m.addZone(z)
	p.cMu.Lock()
	p.children[z] = struct{}{}
	p.cMu.Unlock()
	return id, nil
}

// AddAsset creates an asset in zone with the given initial status.
func (m *Manager) AddAsset(zoneID ZoneID, status AssetStatus) (AssetID, error) {
	z := m.zone(zoneID)
	if z == nil {
		return 0, ErrNotFound
	}
	a := &Asset{}
	a.status.Store(int32(StatusNormal))
	a.zone.Store(z)
	id := AssetID(m.nextAsset.Add(1))
	a.id = id
	m.assetsMu.Lock()
	m.assets[id] = a
	m.assetsMu.Unlock()
	if status != StatusNormal {
		if err := m.SetAssetStatus(id, status); err != nil {
			return 0, err
		}
	}
	return id, nil
}
