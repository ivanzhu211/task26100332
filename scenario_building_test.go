package reservation

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A permanently broken device is shuffled between rooms of one building
// while its room is re-parented between floors of the same building.
// Concurrent Reserve(building) must never succeed, and IsReservable(building)
// must never report true.
func TestBuildingNeverReservableWhileBlockedAssetMoves(t *testing.T) {
	m := NewManager()
	fac := m.AddFacility()
	bld, err := m.AddZone(fac)
	if err != nil {
		t.Fatal(err)
	}
	var floors []ZoneID
	for i := 0; i < 3; i++ {
		f, err := m.AddZone(bld)
		if err != nil {
			t.Fatal(err)
		}
		floors = append(floors, f)
	}
	var rooms []ZoneID
	for i := 0; i < 6; i++ {
		r, err := m.AddZone(floors[i%len(floors)])
		if err != nil {
			t.Fatal(err)
		}
		rooms = append(rooms, r)
	}
	asset, err := m.AddAsset(rooms[0], StatusMaintenance)
	if err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var violated atomic.Bool
	var wg sync.WaitGroup

	// Movers: shuffle the broken asset between rooms of the building.
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := seed
			for !stop.Load() {
				i = (i + 1) % len(rooms)
				if err := m.MoveAsset(asset, rooms[i]); err != nil {
					t.Errorf("MoveAsset: %v", err)
					return
				}
			}
		}(w)
	}
	// Re-parent rooms between floors of the same building.
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := seed
			for !stop.Load() {
				i = (i + 1) % len(rooms)
				r := rooms[i]
				cur := m.zone(r).parent.Load()
				for _, f := range floors {
					if cur != nil && cur.id != f {
						if err := m.MoveZone(r, f); err != nil {
							t.Errorf("MoveZone: %v", err)
							return
						}
						break
					}
				}
			}
		}(w)
	}
	// Reservers: hammer Reserve on the building.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if id, err := m.Reserve(bld); err == nil {
					violated.Store(true)
					m.Release(id)
					return
				}
				if m.IsReservable(bld) {
					violated.Store(true)
					return
				}
			}
		}()
	}

	time.Sleep(2 * time.Second)
	stop.Store(true)
	wg.Wait()
	if violated.Load() {
		t.Fatal("building became reservable while a blocking asset was inside it")
	}
}
