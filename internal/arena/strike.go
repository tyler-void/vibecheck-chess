package arena

import "time"

// Arena-level end reasons that the server does not know about.
const (
	ReasonForfeitIllegal = "forfeit_illegal"
	ReasonForfeitTimeout = "forfeit_timeout"
	ReasonAbortProvider  = "aborted_provider_errors"
	ReasonAbortServer    = "aborted_server_errors"
	ReasonAbortStale     = "aborted_stale_sequence"
)

// Arena retry limits from the rules.
const (
	maxFailures     = 3 // illegal + malformed at one decision; the 3rd forfeits
	maxTimeouts     = 2 // consecutive; the 2nd forfeits
	maxProviderErrs = 5 // consecutive provider errors; the 5th aborts
	maxServerErrs   = 5 // consecutive chess-server transport errors; the 5th aborts
	maxStaleResends = 3 // consecutive stale_sequence resends; the 4th stale aborts
)

// EventKind classifies the outcome of one attempt at a decision.
type EventKind uint8

const (
	EvAccepted  EventKind = iota + 1
	EvIllegal             // server or arena rule rejected the action
	EvMalformed           // no act call, refusal, bad grammar, over-length
	EvMaxTokens           // stopped at max_tokens before calling act (malformed, logged apart)
	EvTimeout             // model request exceeded the decision timeout
	EvProvider            // 429 / 5xx / overloaded / network / model mismatch
	EvServerErr           // chess server unreachable or 5xx
	EvStale               // stale_sequence from the chess server
)

// Event is one attempt outcome with its error code.
type Event struct {
	Kind EventKind
	Code string
}

func Accepted() Event             { return Event{Kind: EvAccepted} }
func Illegal(code string) Event   { return Event{Kind: EvIllegal, Code: code} }
func Malformed(code string) Event { return Event{Kind: EvMalformed, Code: code} }
func MaxTokens() Event            { return Event{Kind: EvMaxTokens, Code: "max_tokens"} }
func Timeout() Event              { return Event{Kind: EvTimeout, Code: "timeout"} }
func ProviderErr(code string) Event {
	return Event{Kind: EvProvider, Code: code}
}
func ServerErr(code string) Event { return Event{Kind: EvServerErr, Code: code} }
func Stale() Event                { return Event{Kind: EvStale, Code: "stale_sequence"} }

// VerdictKind tells the decision loop what to do next.
type VerdictKind uint8

const (
	VDone    VerdictKind = iota + 1
	VAsk                 // ask the model again (after Wait), showing Note if set
	VResend              // resend the same action to the server (after Wait)
	VForfeit             // the seat forfeits the game
	VAbort               // the game ends with no result
)

// Verdict is the strike machine's answer to one event.
type Verdict struct {
	Kind   VerdictKind
	Wait   time.Duration
	Note   string
	Reason string
}

// Strikes is the per-decision strike state machine. It does no I/O.
type Strikes struct {
	failures, timeouts, providerErrs, serverErrs, stale int

	IllegalAttempts int
	MaxTokensStops  int
	TotalTimeouts   int
	ProviderRetries int
	ServerRetries   int
	StaleResends    int

	backoffBase, backoffMax time.Duration
}

// NewStrikes returns a fresh machine for one decision.
func NewStrikes(backoffBase, backoffMax time.Duration) *Strikes {
	return &Strikes{backoffBase: backoffBase, backoffMax: backoffMax}
}

func (s *Strikes) backoff(n int) time.Duration {
	d := s.backoffBase << (n - 1)
	if d > s.backoffMax || d <= 0 {
		d = s.backoffMax
	}
	return d
}

// Next feeds one attempt outcome and returns what to do.
func (s *Strikes) Next(e Event) Verdict {
	switch e.Kind {
	case EvAccepted:
		return Verdict{Kind: VDone}
	case EvIllegal, EvMalformed, EvMaxTokens:
		s.timeouts, s.providerErrs, s.serverErrs, s.stale = 0, 0, 0, 0
		s.failures++
		s.IllegalAttempts++
		if e.Kind == EvMaxTokens {
			s.MaxTokensStops++
		}
		if s.failures >= maxFailures {
			return Verdict{Kind: VForfeit, Reason: ReasonForfeitIllegal}
		}
		return Verdict{Kind: VAsk, Note: "rejected: " + e.Code}
	case EvTimeout:
		s.providerErrs = 0
		s.timeouts++
		s.TotalTimeouts++
		if s.timeouts >= maxTimeouts {
			return Verdict{Kind: VForfeit, Reason: ReasonForfeitTimeout}
		}
		return Verdict{Kind: VAsk, Note: "timeout"}
	case EvProvider:
		s.providerErrs++
		s.ProviderRetries++
		if s.providerErrs >= maxProviderErrs {
			return Verdict{Kind: VAbort, Reason: ReasonAbortProvider}
		}
		return Verdict{Kind: VAsk, Wait: s.backoff(s.providerErrs)}
	case EvServerErr:
		s.serverErrs++
		s.ServerRetries++
		if s.serverErrs >= maxServerErrs {
			return Verdict{Kind: VAbort, Reason: ReasonAbortServer}
		}
		return Verdict{Kind: VResend, Wait: s.backoff(s.serverErrs)}
	case EvStale:
		s.stale++
		s.StaleResends++
		if s.stale > maxStaleResends {
			return Verdict{Kind: VAbort, Reason: ReasonAbortStale}
		}
		return Verdict{Kind: VResend}
	}
	return Verdict{Kind: VAbort, Reason: "aborted_unknown_event"}
}
