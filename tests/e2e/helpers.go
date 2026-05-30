// Package e2e contains end-to-end scaffolds for the KISS-Chess Go backend.
//
// These helpers are intentionally minimal: vanilla net/http + testing only.
// They will be wired to the real server in src/server once it exists.
package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// moveReq mirrors the server-side MoveReq wire shape.
// PlayerID is intentionally omitted: server resolves it from auth.
type moveReq struct {
	GameID       string `json:"game_id"`
	Seq          uint64 `json:"seq"`
	Move         string `json:"move"`
	ClientActionID string `json:"client_action_id"`
	ClaimDraw    bool   `json:"claim_draw"`
}

// sseEvent is one parsed SSE frame.
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// startServer returns an httptest.Server fronting the (future) src/server
// handler. While src/server does not exist, it returns 501 for everything so
// scaffold tests skip cleanly rather than panic.
//
// TODO: replace the placeholder handler with the real router from src/server.
func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := http.NewServeMux()
	h.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "scaffold: src/server not yet implemented", http.StatusNotImplemented)
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// buildMoveReq constructs a JSON-encoded MoveReq body. gameID is supplied by
// the caller (path param and body must agree).
func buildMoveReq(gameID string, seq uint64, move, clientActionID string) []byte {
	b, _ := json.Marshal(moveReq{
		GameID:       gameID,
		Seq:          seq,
		Move:         move,
		ClientActionID: clientActionID,
		ClaimDraw:    false,
	})
	return b
}

// parseSSE reads an SSE stream and yields events to fn until r returns EOF or
// fn returns false. Comment lines (": ping") are skipped.
func parseSSE(r io.Reader, fn func(sseEvent) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Data != "" || ev.Event != "" || ev.ID != "" {
				if !fn(ev) {
					return nil
				}
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, ":"):
			// keepalive comment, ignore
		case strings.HasPrefix(line, "id:"):
			ev.ID = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			if ev.Data != "" {
				ev.Data += "\n"
			}
			ev.Data += strings.TrimSpace(line[5:])
		}
	}
	return sc.Err()
}

// randomGameID returns an opaque hex game id for a single test run.
func randomGameID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "g_" + hex.EncodeToString(b[:])
}

// postMove is a convenience for tests once startServer is wired; today it
// just constructs the request so the call sites compile.
func postMove(srv *httptest.Server, gameID string, body []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s/games/%s/move", srv.URL, gameID)
	return http.Post(url, "application/json", strings.NewReader(string(body)))
}
