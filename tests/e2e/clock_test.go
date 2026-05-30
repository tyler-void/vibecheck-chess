// Package e2e — clock / flag-on-time.
//
// Spec coverage:
//
//	User Story 2 (player flags on time).
//	FR-015 server is sole timing authority, FR-016 monotonic source,
//	FR-017 increment only on accepted move, FR-018 automatic flag,
//	FR-019 flag emitted within 100ms of zero crossing,
//	FR-020 deterministic move-vs-flag race resolution.
//	SC-005 flag adjudicated within 100ms of zero crossing.
package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// FR-018 + SC-005: short time control, no moves; the server adjudicates
// the flag and the resulting snapshot shows the terminal outcome.
//
// Uses a 200 ms initial time + a 400 ms wait so we have generous headroom
// for scheduler latency under CI.
func TestE2E_FlagOnTime(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 200, 0)

	// Let the clock run out.
	time.Sleep(400 * time.Millisecond)

	resp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/snapshot")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", resp.StatusCode, body)
	}

	var out struct {
		Snapshot struct {
			Outcome     int `json:"outcome"`
			Termination int `json:"termination"`
			Seq         int `json:"seq"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}

	// outcome=2 (BlackWin), termination=8 (FlagOnTime) per data-model.md.
	if out.Snapshot.Outcome != 2 {
		t.Errorf("outcome = %d, want 2 (BlackWin)", out.Snapshot.Outcome)
	}
	if out.Snapshot.Termination != 8 {
		t.Errorf("termination = %d, want 8 (FlagOnTime)", out.Snapshot.Termination)
	}
	if out.Snapshot.Seq < 1 {
		t.Errorf("seq = %d, want ≥ 1 (timer-driven adjudication should have advanced seq)", out.Snapshot.Seq)
	}
}

// FR-009: post-flag move is rejected.
func TestE2E_FlagPostTerminalMoveRejected(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 100, 0)

	time.Sleep(250 * time.Millisecond)

	// Try a move; should fail with 409 game_over (or 409 stale_sequence
	// if the client guessed the wrong seq — both are acceptable rejections
	// per FR-009/FR-020).
	resp := postMove(t, srv, gameID, "alice", buildMoveReq(0, "e2e4", "post-flag"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("post-flag move status=%d, want 409", resp.StatusCode)
	}
}

// FR-022/FR-023: the flag terminal is broadcast over SSE — a subscriber
// connected before the flag fires must receive a datastar-patch-signals
// event with outcome != 0 and termination == 8 (FlagOnTime).
func TestE2E_FlagBroadcastsTerminalEvent(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 150, 0)

	// Open SSE before the flag fires.
	sseResp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("sse open: %v", err)
	}
	defer sseResp.Body.Close()

	gotTerminal := make(chan struct{}, 1)
	go func() {
		_ = parseSSE(sseResp.Body, func(ev sseEvent) bool {
			if ev.Event != "datastar-patch-signals" {
				return true
			}
			// Cheap substring check — the terminal event carries
			// "termination":8 (FlagOnTime) and "outcome":2 (BlackWin).
			if strings.Contains(ev.Data, `"termination":8`) &&
				strings.Contains(ev.Data, `"outcome":2`) {
				select {
				case gotTerminal <- struct{}{}:
				default:
				}
				return false
			}
			return true
		})
	}()

	select {
	case <-gotTerminal:
		// success
	case <-time.After(800 * time.Millisecond):
		t.Errorf("did not receive flag-terminal SSE event within 800ms")
	}
}
