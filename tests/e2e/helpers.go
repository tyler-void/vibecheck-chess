// Package e2e contains end-to-end tests for the KISS-Chess Go backend.
// They boot a real in-process server via httptest.NewServer and exercise the
// HTTP + SSE contract end-to-end.
package e2e

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kisschess/internal/chess"
	kisshttp "kisschess/internal/http"
)

// moveReq mirrors the wire shape for POST /games/{id}/move.
// PlayerID is set via the X-Player-ID header, NOT in the body.
type moveReq struct {
	Seq            uint64 `json:"seq"`
	Move           string `json:"move"`
	ClientActionID string `json:"client_action_id"`
	ClaimDraw      bool   `json:"claim_draw,omitempty"`
}

// actionReq is the body for /resign, /draw-offer, and /draw-offer/accept.
type actionReq struct {
	Seq            uint64 `json:"seq"`
	ClientActionID string `json:"client_action_id"`
}

// sseEvent is one parsed SSE frame.
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// startServer returns an httptest.Server fronting the real kisschess HTTP
// server. The server is torn down by t.Cleanup.
func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	mgr := chess.NewGameManager()
	s := kisshttp.NewServer(kisshttp.Options{Manager: mgr})
	s.MarkReady()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return srv
}

// buildMoveReq constructs a JSON-encoded MoveReq body for use with postMove.
func buildMoveReq(seq uint64, move, clientActionID string) []byte {
	b, _ := json.Marshal(moveReq{
		Seq:            seq,
		Move:           move,
		ClientActionID: clientActionID,
	})
	return b
}

// parseSSE reads an SSE stream and yields events to fn until r returns EOF
// or fn returns false. Comment lines (`: ping`) are skipped. Recognises both
// the generic `event: <name>` and the Datastar-specific event names
// (`datastar-patch-elements`, `datastar-patch-signals`) per
// contracts/sse-events.md.
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
			// comment/keepalive, ignore
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

// createGame POSTs /games and returns the assigned game_id.
func createGame(t *testing.T, srv *httptest.Server, white, black string, initialMS, incMS int) string {
	t.Helper()
	body := fmt.Sprintf(`{"white":%q,"black":%q,"time_control":{"initial_ms":%d,"increment_ms":%d}}`,
		white, black, initialMS, incMS)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/games", strings.NewReader(body))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("create game: status=%d body=%s", resp.StatusCode, out)
	}
	var out struct {
		GameID string `json:"game_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return out.GameID
}

// postMove submits a move and returns the response. Caller closes Body.
func postMove(t *testing.T, srv *httptest.Server, gameID, player string, body []byte) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/games/%s/move", srv.URL, gameID)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("X-Player-ID", player)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post move: %v", err)
	}
	return resp
}

// postAction submits to /resign | /draw-offer | /draw-offer/accept.
func postAction(t *testing.T, srv *httptest.Server, gameID, player, endpoint string, body []byte) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/games/%s/%s", srv.URL, gameID, endpoint)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("X-Player-ID", player)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post action: %v", err)
	}
	return resp
}

func actionBody(seq uint64, clientActionID string) []byte {
	b, _ := json.Marshal(actionReq{Seq: seq, ClientActionID: clientActionID})
	return b
}
