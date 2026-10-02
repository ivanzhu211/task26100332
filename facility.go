// Package facility maintains reservable state over a forest of facility
// zones entirely in memory. All operations are safe for concurrent use.
package facility

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Status is the state of an asset. Maintenance and Decommissioned are
// blocking states: any zone whose subtree contains a blocking asset is
// not reservable.
type Status int

const (
	Normal Status = iota
	Maintenance
	Decommissioned
)

func (s Status) blocking() bool { return s != Normal }

// Reservation states of a zone (zone.state).
const (
	resFree      int32 = 0 // no reservation
	resPending   int32 = 1 // a Reserve is in flight on this zone
	resConfirmed int32 = 2 // an active reservation sits on this zone
)

var (
	ErrNotFound      = errors.New("facility: not found")
	ErrAlreadyExists = errors.New("facility: already exists")
	ErrRejected      = errors.New("facility: rejected")
	ErrZoneIsRoot    = errors.New("facility: cannot move a root zone")
	ErrCycle         = errors.New("facility: move would create a cycle")
	ErrReservation   = errors.New("facility: move would nest active reservations")
	ErrInvalidStatus = errors.New("facility: invalid status")
)

// zone is a node of the forest. A zone with nil parent is a facility root.
//
// Concurrency model:
//   - parent is guarded by Manager.structMu (read lock for all normal
//     operations, write lock for MoveZone).
//   - state, subRes, subBlock are atomics; normal operations on unrelated
//     zones never take locks on shared ancestors.
type zone struct {
	id     string
	parent *zone // guarded by structMu

	state    atomic.Int32 // resFree / resPending / resConfirmed
	subRes   atomic.Int64 // active reservations in subtree (incl. self)
	subBlock atomic.Int64 // blocking assets in subtree (incl. self)
}

// asset belongs to exactly one zone. mu serializes operations on this
// asset; counter propagation for an asset op happens while holding mu so
// that per-asset counter contributions never go negative or get lost.
type asset struct {
	mu     sync.Mutex
	id     string
	zone   *zone
	status Status
}

// reservation is an active reservation on a zone.
type reservation struct {
	id   string
	zone *zone
}

// Manager holds the whole forest in memory.
type Manager struct {
	// structMu: normal operations take RLock for their whole duration;
	// MoveZone takes Lock. Readers never block each other, so operations
	// on unrelated zones never wait on a common ancestor.
	structMu sync.RWMutex
	zones    sync.Map // string -> *zone
	assets   sync.Map // string -> *asset
	resvs    sync.Map // string -> *reservation
	nextID   atomic.Uint64
}

// NewManager returns an empty Manager.
func NewManager() *Manager { return &Manager{} }

func (m *Manager) lookupZone(id string) (*zone, error) {
	v, ok := m.zones.Load(id)
	if !ok {
		return nil, fmt.Errorf("%w: zone %q", ErrNotFound, id)
	}
	return v.(*zone), nil
}

func (m *Manager) lookupAsset(id string) (*asset, error) {
	v, ok := m.assets.Load(id)
	if !ok {
		return nil, fmt.Errorf("%w: asset %q", ErrNotFound, id)
	}
	return v.(*asset), nil
}

// chain returns z itself followed by its ancestors up to the root.
// Caller must hold structMu (read or write).
func chain(z *zone) []*zone {
	var out []*zone
	for cur := z; cur != nil; cur = cur.parent {
		out = append(out, cur)
	}
	return out
}

// addBlocked adds delta to the subBlock counter of z and all ancestors.
// Caller must hold structMu (read or write).
func addBlocked(z *zone, delta int64) {
	for cur := z; cur != nil; cur = cur.parent {
		cur.subBlock.Add(delta)
	}
}

// addReserved adds delta to the subRes counter of z and all ancestors.
// Caller must hold structMu (read or write).
func addReserved(z *zone, delta int64) {
	for cur := z; cur != nil; cur = cur.parent {
		cur.subRes.Add(delta)
	}
}

func (m *Manager) newReservationID() string {
	return fmt.Sprintf("r%d", m.nextID.Add(1))
}
