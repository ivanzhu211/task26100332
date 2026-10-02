package reservation

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ZoneID identifies a zone. AssetID identifies an asset. ResID identifies a reservation.
type ZoneID int64
type AssetID int64
type ResID int64

// AssetStatus is the lifecycle state of an asset.
type AssetStatus int32

const (
	StatusNormal      AssetStatus = iota // 正常
	StatusMaintenance                    // 维修中
	StatusDisabled                       // 停用
)

func blocking(s AssetStatus) bool { return s != StatusNormal }

var (
	ErrNotFound            = errors.New("reservation: zone, asset or reservation not found")
	ErrNotReservable       = errors.New("reservation: zone is not reservable")
	ErrCycle               = errors.New("reservation: move would create a cycle")
	ErrRootMove            = errors.New("reservation: cannot move a root (facility) zone")
	ErrReservationConflict = errors.New("reservation: move would nest an active reservation under another")
)

// Zone state packs three counters into one atomic int64 so that
// check-and-set during Reserve is a single CAS:
//
//	bits  0..29: blocked  — blocking assets in this zone's subtree (incl. itself)
//	bits 30..59: subRes   — active reservations strictly below this zone
//	bit  60:     selfRes  — active reservation on this zone itself
const (
	blockedMask = int64(1<<30) - 1
	resUnit     = int64(1) << 30
	resMask     = blockedMask << 30
	selfUnit    = int64(1) << 60
)

func blockedOf(s int64) int64 { return s & blockedMask }
func subResOf(s int64) int64  { return (s & resMask) >> 30 }
func selfOf(s int64) int64 {
	if s&selfUnit != 0 {
		return 1
	}
	return 0
}

// Zone is a node of the facility forest.
type Zone struct {
	id     ZoneID
	mu     sync.RWMutex // path lock: RLock by walking ops, Lock by MoveZone
	parent atomic.Pointer[Zone]
	state  atomic.Int64

	cMu      sync.Mutex
	children map[*Zone]struct{}
}

// Asset belongs to exactly one zone.
type Asset struct {
	id     AssetID
	mu     sync.Mutex // serializes SetAssetStatus/MoveAsset on this asset
	status atomic.Int32
	zone   atomic.Pointer[Zone]
}

// Reservation is one active reservation of a zone.
type Reservation struct {
	ID   ResID
	Zone *Zone
}
