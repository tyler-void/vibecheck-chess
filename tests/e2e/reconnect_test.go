// Package e2e — reconnect / Last-Event-ID resume.
//
// Spec coverage:
//
//	User Story 4 (viewer reconnects and catches up).
//	FR-024 bounded per-room event ring buffer for replay,
//	FR-025 deliver in-order missed events OR a fresh full snapshot when
//	the gap is too large or no starting point is supplied.
package e2e

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// FR-024 / FR-025: a viewer who has received events up to seq N, drops,
// and reconnects with Last-Event-ID: N MUST receive events N+1, N+2, ...
// in order from the ring buffer.
func TestE2E_LastEventIDReplay(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	// Phase 1: connect, see two moves, drop.
	resp1, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("first sse open: %v", err)
	}

	// Drive 2 moves while connected.
	for i, m := range []string{"e2e4", "e7e5"} {
		var p string = "alice"
		if i == 1 {
			p = "bob"
		}
		resp := postMove(t, srv, gameID, p, buildMoveReq(uint64(i), m, "rec-"+m))
		resp.Body.Close()
	}

	// Drain at least 2 events from the first connection.
	seenSeqs := make(chan int, 8)
	var firstWG sync.WaitGroup
	firstWG.Add(1)
	go func() {
		defer firstWG.Done()
		_ = parseSSE(resp1.Body, func(ev sseEvent) bool {
			if ev.Event != "datastar-patch-signals" {
				return true
			}
			if ev.ID != "" {
				var seq int
				_, err := fmt2Atoi(ev.ID, &seq)
				if err == nil {
					select {
					case seenSeqs <- seq:
					default:
					}
				}
			}
			return true
		})
	}()

	// Wait until we've seen both seq=1 and seq=2 (or timeout).
	got := map[int]bool{}
	deadline := time.After(2 * time.Second)
loop:
	for len(got) < 2 {
		select {
		case s := <-seenSeqs:
			got[s] = true
		case <-deadline:
			t.Fatalf("did not receive seq=1 and seq=2 within 2s; got %v", got)
		}
		if got[1] && got[2] {
			break loop
		}
	}

	// Drop the first connection.
	resp1.Body.Close()
	firstWG.Wait()

	// Phase 2: drive 2 MORE moves while disconnected (these are the
	// "missed" events the ring buffer must replay).
	for i, m := range []string{"g1f3", "b8c6"} {
		seq := uint64(i + 2)
		var p string = "alice"
		if i == 1 {
			p = "bob"
		}
		resp := postMove(t, srv, gameID, p, buildMoveReq(seq, m, "rec-late-"+m))
		resp.Body.Close()
	}

	// Phase 3: reconnect with Last-Event-ID: 2 → must replay seq 3, 4.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/games/"+gameID+"/sse", nil)
	req.Header.Set("Last-Event-ID", "2")
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer resp2.Body.Close()

	gotReplay := make(chan int, 8)
	go func() {
		_ = parseSSE(resp2.Body, func(ev sseEvent) bool {
			if ev.Event != "datastar-patch-signals" {
				return true
			}
			if ev.ID == "3" || ev.ID == "4" {
				var seq int
				if _, err := fmt2Atoi(ev.ID, &seq); err == nil {
					select {
					case gotReplay <- seq:
					default:
					}
				}
			}
			return true
		})
	}()

	replaySeen := map[int]bool{}
	deadline = time.After(2 * time.Second)
	for len(replaySeen) < 2 {
		select {
		case s := <-gotReplay:
			replaySeen[s] = true
		case <-deadline:
			t.Fatalf("did not replay seq=3,4 within 2s; got %v", replaySeen)
		}
	}
	if !replaySeen[3] || !replaySeen[4] {
		t.Errorf("replay seqs = %v, want {3,4}", replaySeen)
	}
}

// FR-025: malformed Last-Event-ID MUST NOT 500. The handler treats it as
// "no header" → fresh hydration. (Unit-level coverage in
// internal/sse/handler_test.go, this is the e2e proof.)
func TestE2E_MalformedLastEventIDFallsBackTo200(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/games/"+gameID+"/sse", nil)
	req.Header.Set("Last-Event-ID", "not-a-number")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("sse w/ bad header: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status with malformed Last-Event-ID = %d, want 200", resp.StatusCode)
	}
}

// A fresh connect (no Last-Event-ID header) MUST receive exactly one
// hydration signals patch on connect, regardless of r.seq. This guarantees
// the client has every field of the current snapshot even when the wire
// schema grows (e.g. legal_moves was added in a later release and existed
// in neither initial-state JSON nor an empty replay ring at seq=0).
func TestE2E_FreshConnectHydrates(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	resp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("sse open: %v", err)
	}
	defer resp.Body.Close()

	var got atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = parseSSE(resp.Body, func(ev sseEvent) bool {
			if ev.Event == "datastar-patch-signals" || ev.Event == "datastar-patch-elements" {
				got.Add(1)
			}
			return true
		})
	}()

	time.Sleep(200 * time.Millisecond)
	resp.Body.Close()
	<-done

	if got.Load() != 1 {
		t.Errorf("fresh connect at seq=0 received %d events, want 1 (hydration)", got.Load())
	}
}

// fmt2Atoi is a tiny zero-import helper. Returns 0 + err on garbage.
func fmt2Atoi(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errParse(s)
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}

type errParse string

func (e errParse) Error() string { return "parse: " + string(e) }
