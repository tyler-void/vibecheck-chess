package arena

import (
	"testing"
	"time"
)

// step feeds events and returns the verdict sequence.
func feed(s *Strikes, evs ...Event) []Verdict {
	out := make([]Verdict, 0, len(evs))
	for _, e := range evs {
		out = append(out, s.Next(e))
	}
	return out
}

func kinds(vs []Verdict) []VerdictKind {
	out := make([]VerdictKind, len(vs))
	for i, v := range vs {
		out[i] = v.Kind
	}
	return out
}

func eqKinds(t *testing.T, got []Verdict, want ...VerdictKind) {
	t.Helper()
	g := kinds(got)
	if len(g) != len(want) {
		t.Fatalf("got %v want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("step %d: got %v want %v (all %v)", i, g[i], want[i], g)
		}
	}
}

func TestStrikesTable(t *testing.T) {
	cases := []struct {
		name       string
		evs        []Event
		want       []VerdictKind
		reason     string // reason of the final verdict, if terminal
		illegal    int
		maxTok     int
		timeouts   int
		provider   int
		staleTotal int
	}{
		{"accept first try", []Event{Accepted()}, []VerdictKind{VDone}, "", 0, 0, 0, 0, 0},
		{"two illegal then accept",
			[]Event{Illegal("illegal_move"), Illegal("illegal_move"), Accepted()},
			[]VerdictKind{VAsk, VAsk, VDone}, "", 2, 0, 0, 0, 0},
		{"three illegal forfeits",
			[]Event{Illegal("illegal_move"), Malformed("no_act"), Illegal("own_offer")},
			[]VerdictKind{VAsk, VAsk, VForfeit}, ReasonForfeitIllegal, 3, 0, 0, 0, 0},
		{"max_tokens counts as malformed and separately",
			[]Event{MaxTokens(), MaxTokens(), Accepted()},
			[]VerdictKind{VAsk, VAsk, VDone}, "", 2, 2, 0, 0, 0},
		{"max_tokens x3 forfeits",
			[]Event{MaxTokens(), Malformed("bad_action"), MaxTokens()},
			[]VerdictKind{VAsk, VAsk, VForfeit}, ReasonForfeitIllegal, 3, 2, 0, 0, 0},
		{"timeout then accept",
			[]Event{Timeout(), Accepted()},
			[]VerdictKind{VAsk, VDone}, "", 0, 0, 1, 0, 0},
		{"two consecutive timeouts forfeit",
			[]Event{Timeout(), Timeout()},
			[]VerdictKind{VAsk, VForfeit}, ReasonForfeitTimeout, 0, 0, 2, 0, 0},
		{"timeout streak broken by an illegal answer",
			[]Event{Timeout(), Illegal("illegal_move"), Timeout(), Accepted()},
			[]VerdictKind{VAsk, VAsk, VAsk, VDone}, "", 1, 0, 2, 0, 0},
		{"timeouts do not count as illegal attempts",
			[]Event{Timeout(), Illegal("x"), Timeout(), Illegal("x"), Timeout(), Illegal("x")},
			[]VerdictKind{VAsk, VAsk, VAsk, VAsk, VAsk, VForfeit}, ReasonForfeitIllegal, 3, 0, 3, 0, 0},
		{"provider errors are not strikes",
			[]Event{ProviderErr("overloaded"), ProviderErr("rate_limited"), Illegal("x"), Illegal("x"), Accepted()},
			[]VerdictKind{VAsk, VAsk, VAsk, VAsk, VDone}, "", 2, 0, 0, 2, 0},
		{"five provider errors abort",
			[]Event{ProviderErr("a"), ProviderErr("a"), ProviderErr("a"), ProviderErr("a"), ProviderErr("a")},
			[]VerdictKind{VAsk, VAsk, VAsk, VAsk, VAbort}, ReasonAbortProvider, 0, 0, 0, 5, 0},
		{"provider streak resets on a model answer",
			[]Event{ProviderErr("a"), ProviderErr("a"), ProviderErr("a"), ProviderErr("a"), Illegal("x"),
				ProviderErr("a"), ProviderErr("a"), ProviderErr("a"), ProviderErr("a"), Accepted()},
			[]VerdictKind{VAsk, VAsk, VAsk, VAsk, VAsk, VAsk, VAsk, VAsk, VAsk, VDone}, "", 1, 0, 0, 8, 0},
		{"stale resends up to 3 then aborts",
			[]Event{Stale(), Stale(), Stale(), Stale()},
			[]VerdictKind{VResend, VResend, VResend, VAbort}, ReasonAbortStale, 0, 0, 0, 0, 4},
		{"stale streak resets on accept",
			[]Event{Stale(), Stale(), Stale(), Accepted()},
			[]VerdictKind{VResend, VResend, VResend, VDone}, "", 0, 0, 0, 0, 3},
		{"stale streak resets on an illegal answer",
			[]Event{Stale(), Stale(), Stale(), Illegal("x"), Stale(), Stale(), Stale(), Accepted()},
			[]VerdictKind{VResend, VResend, VResend, VAsk, VResend, VResend, VResend, VDone}, "", 1, 0, 0, 0, 6},
		{"server transport errors resend with backoff, five abort",
			[]Event{ServerErr("network"), ServerErr("http_502"), ServerErr("x"), ServerErr("x"), ServerErr("x")},
			[]VerdictKind{VResend, VResend, VResend, VResend, VAbort}, ReasonAbortServer, 0, 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewStrikes(time.Second, 30*time.Second)
			got := feed(s, c.evs...)
			eqKinds(t, got, c.want...)
			last := got[len(got)-1]
			if last.Reason != c.reason {
				t.Fatalf("reason %q want %q", last.Reason, c.reason)
			}
			if s.IllegalAttempts != c.illegal || s.MaxTokensStops != c.maxTok || s.TotalTimeouts != c.timeouts ||
				s.ProviderRetries != c.provider || s.StaleResends != c.staleTotal {
				t.Fatalf("counters %+v", *s)
			}
		})
	}
}

func TestStrikesAskNotes(t *testing.T) {
	s := NewStrikes(time.Second, 30*time.Second)
	v := s.Next(Illegal("illegal_move"))
	if v.Note == "" {
		t.Fatal("illegal retry must carry a note for the model")
	}
	v = s.Next(Timeout())
	if v.Note == "" {
		t.Fatal("timeout retry must carry a short note")
	}
	if v := s.Next(ProviderErr("x")); v.Note != "" {
		t.Fatal("provider retries must not tell the model anything")
	}
}

func TestStrikesBackoffSchedule(t *testing.T) {
	s := NewStrikes(time.Second, 5*time.Second)
	var waits []time.Duration
	for range 4 {
		waits = append(waits, s.Next(ProviderErr("x")).Wait)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits %v want %v", waits, want)
		}
	}
	if w := s.Next(Stale()).Wait; w != 0 {
		t.Fatalf("stale resend waits %v, want 0", w)
	}
}
