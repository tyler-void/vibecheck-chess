package chess

import (
	"time"

	"kisschess/internal/dsenc"
)

// resetFlagTimerLocked stops any pending flag timer and starts a new one
// that fires when the active side's clock would reach zero. The callback
// captures the room's seq AND the active side at scheduling time so when
// it eventually runs (outside the lock) it can detect stale schedules —
// either a move was accepted (seq advanced) or the side flipped, and the
// timer no longer reflects current reality.
//
// Constitution III: caller MUST hold r.mu.
//
// SC-005: timer interval is exactly ActiveRemaining(now); together with
// onFlagTimer's debit + adjudicate this delivers flag termination within
// scheduler-latency of the actual zero crossing (typically <10 ms on Linux).
func (r *GameRoom) resetFlagTimerLocked(now time.Time) {
	if r.flagTimer != nil {
		r.flagTimer.Stop()
		r.flagTimer = nil
	}
	if r.outcome != Ongoing {
		return // game is terminal — no timer needed
	}

	delay := r.clocks.ActiveRemaining(now)
	if delay < 0 {
		delay = 0
	}

	capturedSeq := r.seq
	capturedColor := r.clocks.Active

	r.flagTimer = time.AfterFunc(delay, func() {
		r.onFlagTimer(capturedSeq, capturedColor)
	})
}

// onFlagTimer is the time.AfterFunc callback. Runs on the timer-heap
// goroutine, NOT under any lock. Re-acquires the room mutex, applies the
// stale-timer guard (seq + Active), and either:
//
//   - Discards the fire (state has moved on);
//   - Reschedules (timer fired marginally early due to scheduler latency
//     and the active side still has remaining time);
//   - Adjudicates the flag, advances seq, snapshots, and broadcasts.
//
// Lock-order discipline (Constitution III): mu is held during the debit +
// adjudication; released BEFORE hub.Publish.
func (r *GameRoom) onFlagTimer(seqAtSchedule uint64, colorAtSchedule Color) {
	now := time.Now()

	r.mu.Lock()

	// Stale-timer guard. Any of these conditions means the schedule that
	// led to this fire is no longer accurate:
	if r.seq != seqAtSchedule ||
		r.clocks.Active != colorAtSchedule ||
		r.outcome != Ongoing {
		r.mu.Unlock()
		return
	}

	// Debit the clock to "now" — the timer may have fired marginally early
	// because of scheduler quantum, or precisely on time.
	r.clocks.Debit(now)
	if r.clocks.Remain[r.clocks.Active] > 0 {
		// False alarm — clock isn't actually zero yet. Reschedule for the
		// remaining duration; loop the same stale-guard the next time it
		// fires. This branch is rare in practice (Go's timer heap typically
		// fires within a few microseconds of the deadline) but it's the
		// correct, robust handling.
		r.resetFlagTimerLocked(now)
		r.mu.Unlock()
		return
	}

	// Genuine flag.
	r.adjudicateFlagLocked() // sets outcome + termination + stops timer
	r.seq++
	snap := r.snapshotLocked(now)
	payload := dsenc.PatchSignalsBytes(snap.Seq, snap.toSignalsJSON())
	r.recent.Append(snap.Seq, SSEEvent{Seq: snap.Seq, Bytes: payload})

	r.mu.Unlock()
	r.hub.Publish(payload)
}
