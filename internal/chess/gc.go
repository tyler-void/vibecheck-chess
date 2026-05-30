package chess

import (
	"context"
	"time"
)

// gcInterval is how often the GameManager scans for evictable rooms.
// One scan every 60s is plenty — a room can only become evictable after
// it has been terminal AND idle for ≥ EvictAfter (default 5 min), so a
// 1-minute sweep introduces at most ~17% slack on the wall-clock retention.
const gcInterval = 1 * time.Minute

// EvictAfter is how long a terminal room must have been BOTH terminal AND
// without any subscribers before the GC sweep evicts it. Five minutes is
// the spec default (research R-22, plan.md GC policy). Exported so tests
// can override it without forking the manager.
var EvictAfter = 5 * time.Minute

// StartGC launches the background eviction goroutine. Returns immediately;
// the goroutine exits when ctx is cancelled.
//
// FR-031: bound memory growth as a function of (active games, connected
// subscribers). A terminal room with no subscribers is dead weight; the
// sweep reclaims it after a grace period long enough that a late-arriving
// spectator can still re-fetch the final state via /snapshot or SSE replay.
func (m *GameManager) StartGC(ctx context.Context) {
	go m.gcLoop(ctx)
}

func (m *GameManager) gcLoop(ctx context.Context) {
	t := time.NewTicker(gcInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.evictTerminalIdle(now)
		}
	}
}

// evictTerminalIdle is the single sweep pass. Public-visible side effect:
// rooms whose outcome has been non-Ongoing for ≥ EvictAfter AND have zero
// current subscribers are removed from the manager. The room itself becomes
// unreachable for GC.
//
// Lock-order: takes the manager's RLock to enumerate, releases it, then
// per-victim takes the room's RLock to check outcome+idle-since, then
// finally takes the manager's Lock to delete. NEVER holds two locks
// simultaneously — Constitution III.
func (m *GameManager) evictTerminalIdle(now time.Time) (evicted []GameID) {
	m.mu.RLock()
	ids := make([]GameID, 0, len(m.rooms))
	for id := range m.rooms {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		// Re-fetch under read lock so a Remove between the enum and the
		// check is harmless.
		m.mu.RLock()
		room, ok := m.rooms[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}

		if !roomIsEvictable(room, now) {
			continue
		}

		// Promote to write lock for the deletion. Re-check under the lock
		// to defend against a racing Get/Create.
		m.mu.Lock()
		if cur, ok := m.rooms[id]; ok && cur == room && roomIsEvictable(room, now) {
			delete(m.rooms, id)
			evicted = append(evicted, id)
		}
		m.mu.Unlock()
	}
	return evicted
}

// roomIsEvictable returns true iff:
//
//   - outcome is terminal (game over), AND
//   - the hub has zero subscribers, AND
//   - the terminal state has been in effect for ≥ EvictAfter.
//
// The "≥ EvictAfter since terminal" check uses the LastTick on the clock —
// after adjudicateFlagLocked / ApplyResign / ApplyDrawAccept the clock's
// LastTick is set to `now`, so it doubles as a "terminal-since" stamp. No
// extra room field needed.
func roomIsEvictable(r *GameRoom, now time.Time) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.outcome == Ongoing {
		return false
	}
	if r.hub.Count() > 0 {
		return false
	}
	if now.Sub(r.clocks.LastTick) < EvictAfter {
		return false
	}
	return true
}
