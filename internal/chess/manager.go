package chess

import "sync"

// GameManager is the per-node registry of active GameRooms.
//
// Concurrency: a single sync.RWMutex protects the map. This is the canonical
// pattern (research R-3) — sync.Map's specialised access patterns don't fit
// here because we maintain invariants beyond a single entry (e.g., "exactly
// one in-memory owner per GameID", Constitution II).
//
// Lock-order discipline (Constitution III): mu protects ONLY the map. Never
// held while writing to a socket. Get returns a *GameRoom; subsequent
// mutations go through the room's own lock.
type GameManager struct {
	mu    sync.RWMutex
	rooms map[GameID]*GameRoom
}

// NewGameManager constructs an empty registry.
func NewGameManager() *GameManager {
	return &GameManager{rooms: make(map[GameID]*GameRoom)}
}

// Get returns the room for id and reports whether it exists. O(1).
func (m *GameManager) Get(id GameID) (*GameRoom, bool) {
	m.mu.RLock()
	r, ok := m.rooms[id]
	m.mu.RUnlock()
	return r, ok
}

// CreateStub registers a room with the given id if and only if id is not
// already taken. Returns the room that "won" — either the freshly-created
// one or the pre-existing one.
//
// Phase 2 uses this stub-aware variant so the HTTP layer skeleton compiles.
// Phase 3 introduces Create(id, white, black, tc) which calls NewGameRoom
// internally and then registers via this same atomic check (T038).
func (m *GameManager) CreateStub(id GameID) *GameRoom {
	// Common path: room doesn't exist. Build the candidate outside the
	// write lock to keep the critical section short.
	candidate := newRoomStub(id)

	m.mu.Lock()
	if existing, ok := m.rooms[id]; ok {
		m.mu.Unlock()
		return existing
	}
	m.rooms[id] = candidate
	m.mu.Unlock()
	return candidate
}

// Remove deletes the room for id from the registry. Idempotent. Does NOT
// tear down the room's subscribers — that's the caller's responsibility
// (Phase 8's GC path, T080).
func (m *GameManager) Remove(id GameID) {
	m.mu.Lock()
	delete(m.rooms, id)
	m.mu.Unlock()
}

// Count returns the number of rooms currently registered. Cheap.
// Used by /metrics (kiss_games_active).
func (m *GameManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}
