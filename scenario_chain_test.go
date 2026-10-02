package reservation

import (
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Concurrent Reserve/Release on zones of one ancestor chain. At no moment
// may two active reservations be in an ancestor-descendant relation.
//
// Detection: each worker records [begin, end] around its hold using a
// shared atomic clock — begin right after Reserve returns, end right
// before Release is called. For zones on one chain, a successful second
// Reserve implies the first reservation's state was already cleaned up,
// which implies its end was already recorded; hence two state-active
// windows on the chain overlap iff their recorded intervals overlap.
func TestChainReserveReleaseInvariant(t *testing.T) {
	m := NewManager()
	fac := m.AddFacility()
	chain := []ZoneID{fac}
	parent := fac
	for i := 0; i < 5; i++ {
		z, err := m.AddZone(parent)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, z)
		parent = z
	}

	type interval struct {
		begin, end int64
	}
	var clock atomic.Int64
	var mu sync.Mutex
	var holds []interval

	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var local []interval
			for !stop.Load() {
				z := chain[rng.Intn(len(chain))]
				if id, err := m.Reserve(z); err == nil {
					b := clock.Add(1)
					time.Sleep(time.Duration(rng.Intn(20)) * time.Microsecond)
					e := clock.Add(1)
					m.Release(id)
					local = append(local, interval{b, e})
				}
			}
			mu.Lock()
			holds = append(holds, local...)
			mu.Unlock()
		}(int64(w) + 1)
	}

	time.Sleep(1500 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	// Every pair of chain zones is ancestor-related, so the invariant is:
	// at most one active hold chain-wide at any moment.
	sort.Slice(holds, func(i, j int) bool { return holds[i].begin < holds[j].begin })
	for i := 1; i < len(holds); i++ {
		if holds[i].begin < holds[i-1].end {
			t.Fatalf("overlapping active reservations on one ancestor chain: %v vs %v",
				holds[i-1], holds[i])
		}
	}
	validate(t, m)
}
