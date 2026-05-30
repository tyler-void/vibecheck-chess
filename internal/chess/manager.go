package chess

import (
	"sync"
	"time"
)

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

// Create registers a fresh room for id with the given participants and time
// control. Returns the room that "won" — if id is already taken (race
// between two POST /games requests with the same id), returns the existing
// room and a `false` second return. Callers that race-lose can detect this
// and either error out (HTTP 409 game_exists) or proceed against the
// existing room.
//
// The room is constructed OUTSIDE the write lock to keep the critical
// section short — NewGameRoom allocates a chess.Game, maps, channel, and a
// FanoutHub, none of which need the manager's mu.
func (m *GameManager) Create(id GameID, white, black PlayerID, tc TimeControl, now time.Time) (*GameRoom, bool) {
	candidate := NewGameRoom(id, white, black, tc, now)

	m.mu.Lock()
	if existing, ok := m.rooms[id]; ok {
		m.mu.Unlock()
		return existing, false
	}
	m.rooms[id] = candidate
	m.mu.Unlock()
	return candidate, true
}

// Remove deletes the room for id from the registry. Idempotent. Does NOT
// tear down the room's subscribers — that's the caller's responsibility
// (Phase 8 GC path, T080).
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
