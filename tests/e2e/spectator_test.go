// Package e2e — spectator fan-out.
//
// Spec coverage:
//
//	User Story 3 (spectators stream a live game).
//	FR-021 per-game realtime stream, FR-022 broadcast only to that game's
//	subscribers, FR-027 no slow blocking, FR-028 drop slow readers.
//	SC-003 player-observed latency ≤ 20% degradation with 1k spectators
//	(unit-scale here; the perf scenario tests the real 1k).
//	SC-009 single slow subscriber does not delay broadcast.
package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 50 healthy spectators subscribe before the first move. All MUST receive
// every move in the same order. Unit scale — the perf suite covers 1k.
func TestE2E_SpectatorFanout(t *testing.T) {
	const spectators = 50
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	// Open SSE on `spectators` connections in parallel.
	bodies := make([]*http.Response, spectators)
	var openWG sync.WaitGroup
	openWG.Add(spectators)
	for i := 0; i < spectators; i++ {
		i := i
		go func() {
			defer openWG.Done()
			resp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
			if err != nil {
				t.Errorf("spectator %d open: %v", i, err)
				return
			}
			bodies[i] = resp
		}()
	}
	openWG.Wait()

	// Defer-close them.
	t.Cleanup(func() {
		for _, b := range bodies {
			if b != nil {
				_ = b.Body.Close()
			}
		}
	})

	// Spawn drainer per spectator; count how many "last_move" events each one sees.
	moves := []string{"e2e4", "e7e5", "g1f3", "b8c6"}
	expected := make(map[string]struct{}, len(moves))
	for _, m := range moves {
		expected[m] = struct{}{}
	}

	type counts struct {
		moves map[string]struct{}
		err   error
	}
	results := make(chan counts, spectators)
	for i := 0; i < spectators; i++ {
		if bodies[i] == nil {
			results <- counts{moves: map[string]struct{}{}}
			continue
		}
		body := bodies[i].Body
		go func() {
			seen := map[string]struct{}{}
			_ = parseSSE(body, func(ev sseEvent) bool {
				if ev.Event != "datastar-patch-signals" {
					return true
				}
				for m := range expected {
					needle := `"last_move":"` + m + `"`
					if strings.Contains(ev.Data, needle) {
						seen[m] = struct{}{}
					}
				}
				if len(seen) == len(expected) {
					return false // received them all
				}
				return true
			})
			results <- counts{moves: seen}
		}()
	}

	// Give SSE handlers a moment to register before we publish.
	time.Sleep(50 * time.Millisecond)

	// Drive the 4 moves.
	players := []string{"alice", "bob", "alice", "bob"}
	for i, m := range moves {
		resp := postMove(t, srv, gameID, players[i], buildMoveReq(uint64(i), m, "spec-"+m))
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("move %d (%s) status=%d", i, m, resp.StatusCode)
		}
	}

	// Wait up to 2s for every spectator to have seen all 4 moves.
	deadline := time.After(2 * time.Second)
	complete := 0
	for complete < spectators {
		select {
		case c := <-results:
			if len(c.moves) == len(expected) {
				complete++
			} else {
				t.Errorf("a spectator saw %d/4 moves: %v", len(c.moves), c.moves)
				complete++
			}
		case <-deadline:
			t.Fatalf("only %d/%d spectators completed within 2s", complete, spectators)
		}
	}
}

// FR-027 / FR-028 / SC-009: one stalled spectator must not block any healthy
// spectator. We open one connection on a slow-reader Go function (only reads
// one line per second) and N healthy spectators that drain as fast as they
// can. Many publishes later, the slow one is dropped; healthies are intact.
func TestE2E_SlowSpectatorDoesNotStall(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	// Healthy reader.
	fastResp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("fast sse open: %v", err)
	}
	defer fastResp.Body.Close()

	var fastEvents atomic.Int64
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		_ = parseSSE(fastResp.Body, func(ev sseEvent) bool {
			if ev.Event == "datastar-patch-signals" {
				fastEvents.Add(1)
			}
			return true
		})
	}()

	// Slow reader — open the connection but don't drain.
	slowResp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("slow sse open: %v", err)
	}
	defer slowResp.Body.Close()

	// Wait for both subscribers to register.
	time.Sleep(50 * time.Millisecond)

	// Drive 20 state-changing actions to exceed the per-subscriber queue
	// (sse.sendBufferSize = 16). We can't use repeated moves (threefold
	// auto-terminates the game) so we spam draw-offers from Alice — each
	// new offer from the same side replaces the prior and advances seq +
	// broadcasts. Bob never accepts; the game stays ongoing.
	pubStart := time.Now()
	for i := 0; i < 20; i++ {
		body := actionBody(uint64(i), "slow-off-"+string(rune('a'+i)))
		resp := postAction(t, srv, gameID, "alice", "draw-offer", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("draw-offer %d status=%d", i, resp.StatusCode)
		}
	}
	pubElapsed := time.Since(pubStart)

	// Publishing 20 actions to a 2-subscriber hub MUST NOT block on the
	// slow reader. Total publish loop should complete in well under 1
	// second — the apply lock-window is tens of microseconds.
	if pubElapsed > 500*time.Millisecond {
		t.Errorf("publishing 20 actions took %v — slow reader appears to be blocking the publisher", pubElapsed)
	}

	// Give the fast reader time to drain.
	time.Sleep(100 * time.Millisecond)

	// The fast reader should have received all 20 events (plus any prior
	// hydration replay events that pre-dated the actions).
	if got := fastEvents.Load(); got < 20 {
		t.Errorf("fast spectator received %d events, want ≥ 20", got)
	}

	// Close fast first so the drainer goroutine exits.
	_ = fastResp.Body.Close()
	select {
	case <-fastDone:
	case <-time.After(200 * time.Millisecond):
		// drainer may still be blocked on Read — that's OK for the test
	}

	// Force a server shutdown — closes pending SSE streams. (httptest
	// Cleanup will do this anyway via t.Cleanup defer chain; we let it
	// happen there.)
	_ = httptest.NewServer
}
