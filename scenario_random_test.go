package reservation

import (
	"math/rand"
	"sync"
	"testing"
)

// Randomly interleaves every operation on a multi-level forest, including
// a concurrent pair of MoveZone calls that would form a cycle if both
// succeeded. After everything stops, the whole forest is recomputed from
// scratch and compared against the library's answers.
func TestRandomMixedConcurrency(t *testing.T) {
	m := NewManager()
	fac := m.AddFacility()

	var zones []ZoneID
	zones = append(zones, fac)
	// facility -> buildings -> floors -> rooms -> partitions
	for b := 0; b < 4; b++ {
		bld, err := m.AddZone(fac)
		if err != nil {
			t.Fatal(err)
		}
		zones = append(zones, bld)
		for f := 0; f < 3; f++ {
			fl, err := m.AddZone(bld)
			if err != nil {
				t.Fatal(err)
			}
			zones = append(zones, fl)
			for r := 0; r < 4; r++ {
				rm, err := m.AddZone(fl)
				if err != nil {
					t.Fatal(err)
				}
				zones = append(zones, rm)
				if r == 0 {
					for p := 0; p < 2; p++ {
						pt, err := m.AddZone(rm)
						if err != nil {
							t.Fatal(err)
						}
						zones = append(zones, pt)
					}
				}
			}
		}
	}

	rng0 := rand.New(rand.NewSource(1))
	var assets []AssetID
	for i := 0; i < 200; i++ {
		st := AssetStatus(rng0.Intn(3))
		id, err := m.AddAsset(zones[rng0.Intn(len(zones))], st)
		if err != nil {
			t.Fatal(err)
		}
		assets = append(assets, id)
	}

	// Concurrent pair of MoveZone that would form a cycle if both
	// succeeded. Run them on their own barrier, before the random chaos,
	// so the "not both succeeded" assertion is deterministic.
	a, b := zones[1], zones[2]
	pairStart := make(chan struct{})
	var cycWg sync.WaitGroup
	cycOK := make([]bool, 2)
	for i, pair := range [][2]ZoneID{{a, b}, {b, a}} {
		cycWg.Add(1)
		go func(idx int, from, to ZoneID) {
			defer cycWg.Done()
			<-pairStart
			cycOK[idx] = m.MoveZone(from, to) == nil
		}(i, pair[0], pair[1])
	}
	close(pairStart)
	cycWg.Wait()
	if cycOK[0] && cycOK[1] {
		t.Fatal("both cycle-forming MoveZone calls succeeded")
	}

	const workers = 6
	const ops = 3000
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			<-start
			rng := rand.New(rand.NewSource(seed))
			var held []ResID
			for i := 0; i < ops; i++ {
				switch rng.Intn(6) {
				case 0:
					m.SetAssetStatus(assets[rng.Intn(len(assets))], AssetStatus(rng.Intn(3)))
				case 1:
					m.MoveAsset(assets[rng.Intn(len(assets))], zones[rng.Intn(len(zones))])
				case 2:
					// Non-root zones only; errors (cycle, conflicts) are fine.
					m.MoveZone(zones[1+rng.Intn(len(zones)-1)], zones[rng.Intn(len(zones))])
				case 3:
					if id, err := m.Reserve(zones[rng.Intn(len(zones))]); err == nil {
						held = append(held, id)
					}
				case 4:
					if len(held) > 0 {
						j := rng.Intn(len(held))
						m.Release(held[j])
						held = append(held[:j], held[j+1:]...)
					}
				case 5:
					m.IsReservable(zones[rng.Intn(len(zones))])
				}
			}
			for _, id := range held {
				m.Release(id)
			}
		}(int64(w) + 2)
	}
	close(start)
	wg.Wait()
	validate(t, m)
}
