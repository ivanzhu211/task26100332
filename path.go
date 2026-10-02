package reservation

// maxDepth is a sanity bound; the forest is documented to stay shallow
// (depth <= 8 in practice). Exceeding it means a cycle slipped in, which
// is a bug worth panicking over instead of spinning forever.
const maxDepth = 1 << 20

// lockPath RLocks every zone on the path from the root down to z and
// returns the path (root first, z last).
//
// The walk is recorded lock-free, then locked top-down (the same order
// MoveZone uses for its write locks, so no lock-ordering deadlock is
// possible), then verified. If a concurrent MoveZone changed the ancestry
// in between, everything is unlocked and the whole attempt retries.
func (m *Manager) lockPath(z *Zone) []*Zone {
	for {
		path := chainToRoot(z)
		for _, n := range path {
			n.mu.RLock()
		}
		// Verify: re-walk from z up and compare against the recorded path.
		cur := z
		ok := true
		for i := len(path) - 1; i >= 0; i-- {
			if path[i] != cur {
				ok = false
				break
			}
			cur = cur.parent.Load()
		}
		if ok && cur == nil {
			return path
		}
		unlockPath(path)
	}
}

func unlockPath(path []*Zone) {
	for _, n := range path {
		n.mu.RUnlock()
	}
}

// chainToRoot returns the path from the root down to z, without locking.
// Callers must ensure ancestry is stable (hold moveMu, or tolerate retries).
func chainToRoot(z *Zone) []*Zone {
	var rev []*Zone
	for cur := z; cur != nil; cur = cur.parent.Load() {
		rev = append(rev, cur)
		if len(rev) > maxDepth {
			panic("reservation: cycle detected in zone forest")
		}
	}
	path := make([]*Zone, len(rev))
	for i, n := range rev {
		path[len(rev)-1-i] = n
	}
	return path
}
