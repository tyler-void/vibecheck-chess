package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"time"

	"kisschess/internal/chess"
)

// playerIDHeader is the (placeholder) header the gateway sets after
// authenticating the request. Real auth lives upstream of this engine —
// contracts/http-api.md is explicit about this.
const playerIDHeader = "X-Player-ID"

// errorEnvelope is the JSON-fallback error shape per contracts/http-api.md.
// The Datastar response path lives in a separate helper (Phase 9 polish).
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code            string               `json:"code"`
	Message         string               `json:"message"`
	CurrentSnapshot *chess.BoardSnapshot `json:"current_snapshot,omitempty"`
}

// writeJSONError renders the standard error envelope. status is the HTTP
// status code; code is the machine-readable error code from the contract.
// snap is optional (nil omits it) — included for stale_sequence, game_over,
// flagged to let clients reconcile in one round trip.
func writeJSONError(w stdhttp.ResponseWriter, status int, code, msg string, snap *chess.BoardSnapshot) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: errorBody{Code: code, Message: msg, CurrentSnapshot: snap},
	})
}

// === POST /games — create a game =====================================

// createGameReq is the JSON body for game creation.
type createGameReq struct {
	GameID      chess.GameID    `json:"game_id,omitempty"` // optional; server assigns if missing
	White       chess.PlayerID  `json:"white"`
	Black       chess.PlayerID  `json:"black"`
	TimeControl timeControlJSON `json:"time_control"`
}

type timeControlJSON struct {
	InitialMS   int64 `json:"initial_ms"`
	IncrementMS int64 `json:"increment_ms"`
	DelayMS     int64 `json:"delay_ms"`
}

type createGameResp struct {
	GameID   chess.GameID        `json:"game_id"`
	Snapshot chess.BoardSnapshot `json:"snapshot"`
}

func (s *Server) handleCreateGame(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body createGameReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "malformed JSON body", nil)
		return
	}

	if body.White == "" || body.Black == "" {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "white and black are required", nil)
		return
	}
	if body.White == body.Black {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "white and black must differ", nil)
		return
	}

	tc := chess.TimeControl{
		Initial:   time.Duration(body.TimeControl.InitialMS) * time.Millisecond,
		Increment: time.Duration(body.TimeControl.IncrementMS) * time.Millisecond,
		Delay:     time.Duration(body.TimeControl.DelayMS) * time.Millisecond,
	}
	if err := tc.Validate(); err != nil {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}

	id := body.GameID
	if id == "" {
		id = chess.GameID(randomGameID())
	}

	now := time.Now()
	room, created := s.Manager.Create(id, body.White, body.Black, tc, now)
	if !created {
		writeJSONError(w, stdhttp.StatusConflict, "game_exists", "game id already exists", nil)
		return
	}

	resp := createGameResp{
		GameID:   room.ID(),
		Snapshot: room.Snapshot(now),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(stdhttp.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// randomGameID returns a 16-hex-char id sufficient for collision-free game
// ids at our scale (10k games × birthday-bound; the LB sticky routes by
// this id so even tiny collisions don't cross-contaminate state).
func randomGameID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// === GET /games/{id}/snapshot — current state =========================

type snapshotResp struct {
	GameID   chess.GameID        `json:"game_id"`
	Snapshot chess.BoardSnapshot `json:"snapshot"`
}

func (s *Server) handleSnapshot(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id := chess.GameID(r.PathValue("id"))
	room, ok := s.Manager.Get(id)
	if !ok {
		writeJSONError(w, stdhttp.StatusNotFound, "game_not_found", "no game with that id", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(stdhttp.StatusOK)
	_ = json.NewEncoder(w).Encode(snapshotResp{
		GameID:   id,
		Snapshot: room.Snapshot(time.Now()),
	})
}

// === POST /games/{id}/move — submit a move ===========================

type moveReqJSON struct {
	Seq            uint64 `json:"seq"`
	Move           string `json:"move"`
	ClientActionID string `json:"client_action_id"`
	ClaimDraw      bool   `json:"claim_draw,omitempty"`
}

// moveSuccessJSON is the JSON-fallback success body on idempotent retry
// (200 OK with Accept: application/json). The default success path returns
// 204 No Content and the resulting state arrives on the SSE stream.
type moveSuccessJSON struct {
	Result string `json:"result"`
	Seq    uint64 `json:"seq"`
}

func (s *Server) handleMove(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id := chess.GameID(r.PathValue("id"))
	room, ok := s.Manager.Get(id)
	if !ok {
		writeJSONError(w, stdhttp.StatusNotFound, "game_not_found", "no game with that id", nil)
		return
	}

	player := chess.PlayerID(r.Header.Get(playerIDHeader))
	if player == "" {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "missing "+playerIDHeader+" header", nil)
		return
	}

	var body moveReqJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "malformed JSON body", nil)
		return
	}
	if body.ClientActionID == "" || len(body.ClientActionID) > 64 {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "client_action_id must be 1-64 bytes", nil)
		return
	}
	// Pre-flight syntax check so a bad-shape move never enters the room.
	if err := chess.ValidateUCISyntax(body.Move); err != nil {
		writeJSONError(w, stdhttp.StatusBadRequest, "bad_move_syntax", "move must match UCI long-algebraic regex", nil)
		return
	}

	now := time.Now()
	req := chess.MoveReq{
		PlayerID:       player,
		Move:           body.Move,
		ClientActionID: body.ClientActionID,
		Seq:            body.Seq,
		ClaimDraw:      body.ClaimDraw,
	}

	result, err := room.ApplyMove(r.Context(), req, now)
	if err != nil {
		s.writeMoveError(w, r, err, room, now)
		return
	}

	if result.Idempotent {
		// Idempotent retry — the original broadcast already went out on
		// SSE; nothing to re-emit. 200 + JSON envelope on Accept:json;
		// 200 with empty body otherwise (Datastar client reconciles from
		// the open SSE stream).
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(stdhttp.StatusOK)
			_ = json.NewEncoder(w).Encode(moveSuccessJSON{Result: "ok", Seq: result.Seq})
			return
		}
		w.WriteHeader(stdhttp.StatusOK)
		return
	}
	// Fresh accept — 204 No Content; state arrives on the SSE stream.
	w.WriteHeader(stdhttp.StatusNoContent)
}

// writeMoveError maps a chess.Apply* error to its contract-defined HTTP
// status + code. ErrStaleSequence / ErrGameOver / ErrFlagged include
// current_snapshot for one-round-trip reconciliation.
func (s *Server) writeMoveError(w stdhttp.ResponseWriter, _ *stdhttp.Request, err error, room *chess.GameRoom, now time.Time) {
	var (
		status int
		code   string
		snap   *chess.BoardSnapshot
	)
	switch err {
	case chess.ErrBadMoveSyntax:
		status, code = stdhttp.StatusBadRequest, "bad_move_syntax"
	case chess.ErrNotParticipant:
		status, code = stdhttp.StatusConflict, "not_a_participant"
	case chess.ErrNotYourTurn:
		s2 := room.Snapshot(now)
		status, code, snap = stdhttp.StatusConflict, "not_your_turn", &s2
	case chess.ErrStaleSequence:
		s2 := room.Snapshot(now)
		status, code, snap = stdhttp.StatusConflict, "stale_sequence", &s2
	case chess.ErrGameOver:
		s2 := room.Snapshot(now)
		status, code, snap = stdhttp.StatusConflict, "game_over", &s2
	case chess.ErrFlagged:
		s2 := room.Snapshot(now)
		status, code, snap = stdhttp.StatusConflict, "flagged", &s2
	case chess.ErrIllegalMove:
		status, code = stdhttp.StatusUnprocessableEntity, "illegal_move"
	default:
		status, code = stdhttp.StatusInternalServerError, "internal_error"
	}
	writeJSONError(w, status, code, err.Error(), snap)
}

// wantsJSON returns true iff the request's Accept header prefers
// application/json — i.e., a curl/k6/server-to-server client rather than a
// Datastar-driven browser.
func wantsJSON(r *stdhttp.Request) bool {
	a := r.Header.Get("Accept")
	return strings.Contains(a, "application/json")
}
