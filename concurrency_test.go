package facility

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// recompute walks the whole forest from scratch and checks the library's
// answers against the definition. Call only when quiescent.
func recompute(t *testing.T, m *Manager) {
	t.Helper()

	zones := map[string]*zone{}
	m.zones.Range(func(k, v any) bool {
		zones[k.(string)] = v.(*zone)
		return true
	})

	// Parent chains must be acyclic and reach a facility root.
	for id, z := range zones {
		seen := map[*zone]bool{}
		cur := z
		for cur != nil {
			if seen[cur] {
				t.Fatalf("cycle detected on parent chain of zone %s", id)
			}
			seen[cur] = true
			cur = cur.parent
		}
	}

	// Expected subtree aggregates, computed from scratch.
	expBlock := map[*zone]int64{}
	expRes := map[*zone]int64{}
	m.assets.Range(func(k, v any) bool {
		a := v.(*asset)
		a.mu.Lock()
		st, z := a.status, a.zone
		a.mu.Unlock()
		if st.blocking() {
			for cur := z; cur != nil; cur = cur.parent {
				expBlock[cur]++
			}
		}
		return true
	})
	for _, z := range zones {
		if z.state.Load() == resConfirmed {
			for cur := z; cur != nil; cur = cur.parent {
				expRes[cur]++
			}
		}
	}

	for id, z := range zones {
		if got := z.subBlock.Load(); got != expBlock[z] {
			t.Fatalf("zone %s: subBlock = %d, recomputed %d", id, got, expBlock[z])
		}
		if got := z.subRes.Load(); got != expRes[z] {
			t.Fatalf("zone %s: subRes = %d, recomputed %d", id, got, expRes[z])
		}
		want := expBlock[z] == 0 && expRes[z] == 0
		for cur := z.parent; cur != nil && want; cur = cur.parent {
			if cur.state.Load() != resFree {
				want = false
			}
		}
		got, err := m.IsReservable(id)
		if err != nil {
			t.Fatalf("IsReservable(%s): %v", id, err)
		}
		if got != want {
			t.Fatalf("IsReservable(%s) = %v, recomputed %v", id, got, want)
		}
	}
}

// Scenario 1: a device stays in maintenance inside one building while it
// is moved between rooms and its room is moved between floors. The
// building must never become reservable, not even for an instant.
func TestBuildingNeverReservable(t *testing.T) {
	m := NewManager()
	mustAddZone(t, m, "F", "")
	mustAddZone(t, m, "B", "F")
	mustAddZone(t, m, "L1", "B")
	mustAddZone(t, m, "L2", "B")
	rooms := []string{"R1", "R2", "R3", "R4"}
	mustAddZone(t, m, "R1", "L1")
	mustAddZone(t, m, "R2", "L1")
	mustAddZone(t, m, "R3", "L2")
	mustAddZone(t, m, "R4", "L2")
	if err := m.AddAsset("A", "R1"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAssetStatus("A", Maintenance); err != nil {
		t.Fatal(err)
	}

	var curRoom atomic.Value
	curRoom.Store("R1")

	var stop atomic.Int64 // counts finished mover iterations
	const movers = 4
	const iterations = 400

	var wg sync.WaitGroup
	for i := 0; i < movers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(i)))
			for j := 0; j < iterations; j++ {
				if i%2 == 0 {
					// Move the broken device to another room of the building.
					to := rooms[rng.Intn(len(rooms))]
					if err := m.MoveAsset("A", to); err == nil {
						curRoom.Store(to)
					}
				} else {
					// Re-hang the room holding the device onto a floor.
					room := curRoom.Load().(string)
					floor := "L1"
					if rng.Intn(2) == 0 {
						floor = "L2"
					}
					_ = m.MoveZone(room, floor)
				}
				stop.Add(1)
			}
		}(i)
	}

	// Reservers hammer the building until all movers are done.
	var resWg sync.WaitGroup
	for i := 0; i < 4; i++ {
		resWg.Add(1)
		go func() {
			defer resWg.Done()
			for stop.Load() < movers*iterations {
				if id, err := m.Reserve("B"); err == nil {
					t.Errorf("Reserve(B) succeeded (id=%s) while broken device inside", id)
					_ = m.Release(id)
					return
				}
				if ok, err := m.IsReservable("B"); err == nil && ok {
					t.Error("IsReservable(B) = true while broken device inside")
					return
				}
			}
		}()
	}
	wg.Wait()
	resWg.Wait()

	// The device must still be inside the building and blocking it.
	if ok, _ := m.IsReservable("B"); ok {
		t.Fatal("B reservable after moves, device escaped?")
	}
	recompute(t, m)
}

// Scenario 2: random concurrent mix of every operation, including a pair
// of concurrent MoveZones that would form a cycle if both succeeded.
// Afterwards the whole forest is recomputed from scratch and compared.
func TestRandomConcurrentOps(t *testing.T) {
	m := NewManager()
	const facilities = 3
	for i := 0; i < facilities; i++ {
		mustAddZone(t, m, fmt.Sprintf("F%d", i), "")
	}

	var mu sync.Mutex // guards the test-side bookkeeping below
	var zoneIDs []string
	var assetIDs []string
	var activeResvs []string
	for i := 0; i < facilities; i++ {
		zoneIDs = append(zoneIDs, fmt.Sprintf("F%d", i))
	}

	// Pre-build a tree so most operations have targets.
	rng0 := rand.New(rand.NewSource(1))
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("Z%d", i)
		parent := zoneIDs[rng0.Intn(len(zoneIDs))]
		if err := m.AddZone(id, parent); err == nil {
			zoneIDs = append(zoneIDs, id)
		}
	}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("A%d", i)
		if err := m.AddAsset(id, zoneIDs[rng0.Intn(len(zoneIDs))]); err == nil {
			assetIDs = append(assetIDs, id)
		}
	}

	// Pick two unrelated subtrees for the cyclic MoveZone pair.
	mustAddZone(t, m, "CX", "F0")
	mustAddZone(t, m, "CXc", "CX")
	mustAddZone(t, m, "CY", "F1")
	mustAddZone(t, m, "CYc", "CY")

	const workers = 8
	const opsPerWorker = 1500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerWorker; i++ {
				mu.Lock()
				z := zoneIDs[rng.Intn(len(zoneIDs))]
				z2 := zoneIDs[rng.Intn(len(zoneIDs))]
				var resv string
				if len(activeResvs) > 0 {
					resv = activeResvs[rng.Intn(len(activeResvs))]
				}
				a := assetIDs[rng.Intn(len(assetIDs))]
				mu.Unlock()

				switch rng.Intn(10) {
				case 0:
					id := fmt.Sprintf("Zx%d_%d", seed, i)
					if err := m.AddZone(id, z); err == nil {
						mu.Lock()
						zoneIDs = append(zoneIDs, id)
						mu.Unlock()
					}
				case 1:
					id := fmt.Sprintf("Ax%d_%d", seed, i)
					if err := m.AddAsset(id, z); err == nil {
						mu.Lock()
						assetIDs = append(assetIDs, id)
						mu.Unlock()
					}
				case 2:
					_ = m.SetAssetStatus(a, Status(rng.Intn(3)))
				case 3:
					_ = m.MoveAsset(a, z)
				case 4:
					_ = m.MoveZone(z, z2)
				case 5, 6:
					if id, err := m.Reserve(z); err == nil {
						mu.Lock()
						activeResvs = append(activeResvs, id)
						mu.Unlock()
					}
				case 7:
					if resv != "" {
						if err := m.Release(resv); err == nil {
							mu.Lock()
							for j, r := range activeResvs {
								if r == resv {
									activeResvs = append(activeResvs[:j], activeResvs[j+1:]...)
									break
								}
							}
							mu.Unlock()
						}
					}
				default:
					_, _ = m.IsReservable(z)
				}
			}
		}(int64(w) + 2)
	}

	// The cyclic pair: moving CX under CY's child and CY under CX's child.
	// Both succeeding would create a cycle; at most one may succeed.
	var cycWg sync.WaitGroup
	cycResults := make([]error, 2)
	cycWg.Add(2)
	go func() { defer cycWg.Done(); cycResults[0] = m.MoveZone("CX", "CYc") }()
	go func() { defer cycWg.Done(); cycResults[1] = m.MoveZone("CY", "CXc") }()

	wg.Wait()
	cycWg.Wait()

	succ := 0
	for _, err := range cycResults {
		if err == nil {
			succ++
		}
	}
	if succ > 1 {
		t.Fatalf("both cyclic MoveZones succeeded: %v", cycResults)
	}

	recompute(t, m)
}

// Scenario 3: concurrent Reserve/Release on zones of one ancestor chain.
// At no time may two active (confirmed) reservations be in an
// ancestor/descendant relation.
func TestChainReserveRelease(t *testing.T) {
	m := NewManager()
	chainIDs := []string{"C0", "C1", "C2", "C3", "C4", "C5"}
	mustAddZone(t, m, "C0", "")
	for i := 1; i < len(chainIDs); i++ {
		mustAddZone(t, m, chainIDs[i], chainIDs[i-1])
	}
	chainZones := make([]*zone, len(chainIDs))
	for i, id := range chainIDs {
		v, _ := m.zones.Load(id)
		chainZones[i] = v.(*zone)
	}

	const workers = 6
	const opsPerWorker = 3000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var held []string
			for i := 0; i < opsPerWorker; i++ {
				if len(held) > 0 && rng.Intn(2) == 0 {
					id := held[rng.Intn(len(held))]
					if err := m.Release(id); err == nil {
						for j, r := range held {
							if r == id {
								held = append(held[:j], held[j+1:]...)
								break
							}
						}
					}
				} else {
					zi := rng.Intn(len(chainIDs))
					if id, err := m.Reserve(chainIDs[zi]); err == nil {
						// We hold this reservation until Release, so our
						// zone stays confirmed throughout this check: any
						// other confirmed zone on the chain is a genuine
						// simultaneous ancestor/descendant violation.
						for j, cz := range chainZones {
							if j != zi && cz.state.Load() == resConfirmed {
								t.Errorf("confirmed reservations on both %s and %s",
									chainIDs[zi], chainIDs[j])
							}
						}
						held = append(held, id)
					}
				}
			}
			for _, id := range held {
				_ = m.Release(id)
			}
		}(int64(w) + 100)
	}
	wg.Wait()

	recompute(t, m)
}
