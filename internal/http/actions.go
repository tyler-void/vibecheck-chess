package http

import (
	"encoding/json"
	stdhttp "net/http"
	"time"

	"kisschess/internal/chess"
)

// actionReqJSON is the body shape for /resign, /draw-offer, and
// /draw-offer/accept per contracts/http-api.md.
type actionReqJSON struct {
	Seq            uint64 `json:"seq"`
	ClientActionID string `json:"client_action_id"`
}

type actionSuccessJSON struct {
	Result string `json:"result"`
	Seq    uint64 `json:"seq"`
}

// decodeAction returns the parsed body + the room or a written error. On
// any error case the response has already been written; caller returns
// immediately.
func (s *Server) decodeAction(w stdhttp.ResponseWriter, r *stdhttp.Request) (*chess.GameRoom, chess.PlayerID, actionReqJSON, bool) {
	id := chess.GameID(r.PathValue("id"))
	room, ok := s.Manager.Get(id)
	if !ok {
		writeJSONError(w, stdhttp.StatusNotFound, "game_not_found", "no game with that id", nil)
		return nil, "", actionReqJSON{}, false
	}
	player := chess.PlayerID(r.Header.Get(playerIDHeader))
	if player == "" {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "missing "+playerIDHeader+" header", nil)
		return nil, "", actionReqJSON{}, false
	}
	var body actionReqJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "malformed JSON body", nil)
		return nil, "", actionReqJSON{}, false
	}
	if body.ClientActionID == "" || len(body.ClientActionID) > 64 {
		writeJSONError(w, stdhttp.StatusBadRequest, "invalid_request", "client_action_id must be 1-64 bytes", nil)
		return nil, "", actionReqJSON{}, false
	}
	return room, player, body, true
}

// writeActionError maps a chess.Apply* error to its contract status + code.
// Shared by resign/draw-offer/draw-accept since their error surfaces are
// nearly identical.
func (s *Server) writeActionError(w stdhttp.ResponseWriter, err error, room *chess.GameRoom, now time.Time) {
	var (
		status int
		code   string
		snap   *chess.BoardSnapshot
	)
	switch err {
	case chess.ErrNotParticipant:
		status, code = stdhttp.StatusConflict, "not_a_participant"
	case chess.ErrStaleSequence:
		s2 := room.Snapshot(now)
		status, code, snap = stdhttp.StatusConflict, "stale_sequence", &s2
	case chess.ErrGameOver:
		s2 := room.Snapshot(now)
		status, code, snap = stdhttp.StatusConflict, "game_over", &s2
	case chess.ErrNoPendingOffer:
		status, code = stdhttp.StatusConflict, "no_pending_offer"
	case chess.ErrOwnOffer:
		status, code = stdhttp.StatusConflict, "own_offer"
	default:
		status, code = stdhttp.StatusInternalServerError, "internal_error"
	}
	writeJSONError(w, status, code, err.Error(), snap)
}

// writeActionSuccess emits the appropriate body. New accept → 204; idempotent
// retry → 200 + JSON envelope (when Accept: application/json) or 200 empty.
func writeActionSuccess(w stdhttp.ResponseWriter, r *stdhttp.Request, result chess.ActionResult) {
	if result.Idempotent {
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(stdhttp.StatusOK)
			_ = json.NewEncoder(w).Encode(actionSuccessJSON{Result: "ok", Seq: result.Seq})
			return
		}
		w.WriteHeader(stdhttp.StatusOK)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}

// POST /games/{id}/resign
func (s *Server) handleResign(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	room, player, body, ok := s.decodeAction(w, r)
	if !ok {
		return
	}
	now := time.Now()
	result, err := room.ApplyResign(r.Context(), chess.ActionReq{
		PlayerID:       player,
		ClientActionID: body.ClientActionID,
		Seq:            body.Seq,
	}, now)
	if err != nil {
		s.writeActionError(w, err, room, now)
		return
	}
	writeActionSuccess(w, r, result)
}

// POST /games/{id}/draw-offer
func (s *Server) handleDrawOffer(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	room, player, body, ok := s.decodeAction(w, r)
	if !ok {
		return
	}
	now := time.Now()
	result, err := room.ApplyDrawOffer(r.Context(), chess.ActionReq{
		PlayerID:       player,
		ClientActionID: body.ClientActionID,
		Seq:            body.Seq,
	}, now)
	if err != nil {
		s.writeActionError(w, err, room, now)
		return
	}
	writeActionSuccess(w, r, result)
}

// POST /games/{id}/draw-offer/accept
func (s *Server) handleDrawAccept(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	room, player, body, ok := s.decodeAction(w, r)
	if !ok {
		return
	}
	now := time.Now()
	result, err := room.ApplyDrawAccept(r.Context(), chess.ActionReq{
		PlayerID:       player,
		ClientActionID: body.ClientActionID,
		Seq:            body.Seq,
	}, now)
	if err != nil {
		s.writeActionError(w, err, room, now)
		return
	}
	writeActionSuccess(w, r, result)
}
