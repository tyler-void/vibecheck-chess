// Package e2e — proxy survival (US5 / FR-026 / SC-010).
//
// CI gate: the Go-level verification — SSE headers and keep-alive emission
// cadence work as configured at the source. The full nginx-in-the-loop
// smoke test lives in quickstart.md as a manual pre-release procedure
// (per the U2 remediation veto on docker-compose-in-CI).
package e2e

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// FR-026 / research R-7: every SSE response MUST set the four required
// headers. Missing any one of them causes some proxy on the path to
// either buffer the response (Cache-Control / no-transform), drop the
// connection at the next idle window (Connection: keep-alive), or
// collapse the stream (X-Accel-Buffering for nginx).
func TestE2E_SSEResponseHeaders(t *testing.T) {
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 60000, 0)

	resp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("sse open: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	checks := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache, no-transform",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for header, want := range checks {
		got := resp.Header.Get(header)
		if got != want {
			t.Errorf("SSE %s = %q, want %q", header, got, want)
		}
	}

	// Bonus: NO Content-Length on a streaming response — that would make
	// the response a fixed-size payload and proxies could decide to
	// buffer until "complete". (Go's net/http drops Content-Length on
	// flushed responses automatically; we verify the behaviour holds.)
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		t.Errorf("SSE Content-Length = %q, want empty (streaming response)", cl)
	}
}

// FR-026 / SC-010: the 15-second `: ping` cadence keeps long-idle streams
// alive across proxy timeouts. We can't sit for 15s in CI, so this test
// just asserts that an open SSE connection on a game with NO activity
// stays open and produces at least one ping comment within a generous
// 18-second window. Skipped under -short.
func TestE2E_KeepAlivePingCadence(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: 18s cadence test skipped")
	}
	srv := startServer(t)
	gameID := createGame(t, srv, "alice", "bob", 5*60*1000, 0) // 5 min, won't flag

	resp, err := srv.Client().Get(srv.URL + "/games/" + gameID + "/sse")
	if err != nil {
		t.Fatalf("sse open: %v", err)
	}
	defer resp.Body.Close()

	var pings atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The parseSSE helper drops comment lines (": ping") on purpose
		// because most callers don't care about them. For this test we
		// need to see them — read raw lines instead.
		buf := make([]byte, 256)
		for {
			n, err := resp.Body.Read(buf)
			if err != nil {
				return
			}
			s := string(buf[:n])
			if strings.Contains(s, ": ping") {
				pings.Add(1)
			}
		}
	}()

	// Wait 18 seconds — one full 15s cadence + a 3s headroom.
	time.Sleep(18 * time.Second)
	resp.Body.Close()
	<-done

	if pings.Load() < 1 {
		t.Errorf("got %d ping comments in 18s, want ≥ 1", pings.Load())
	}
}
