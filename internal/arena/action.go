package arena

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Talk and memo limits, in runes.
const (
	MaxTalkRunes = 140
	MaxMemoRunes = 280
)

// ActionKind is one entry of the chess action grammar.
type ActionKind uint8

const (
	KindMove ActionKind = iota + 1
	KindClaimDraw
	KindResign
	KindOfferDraw
	KindAcceptDraw
)

// Action is a syntactically valid action. Legality is the server's call.
type Action struct {
	Kind  ActionKind
	UCI   string // KindMove only
	Claim bool   // KindMove only: "<uci> claim"
	Talk  string
	Memo  string
}

func (a Action) String() string {
	switch a.Kind {
	case KindMove:
		if a.Claim {
			return a.UCI + " claim"
		}
		return a.UCI
	case KindClaimDraw:
		return "claim_draw"
	case KindResign:
		return "resign"
	case KindOfferDraw:
		return "offer_draw"
	case KindAcceptDraw:
		return "accept_draw"
	}
	return "?"
}

// MalformedError is a syntax failure. It counts as a malformed attempt.
type MalformedError struct {
	Code   string
	Reason string
}

func (e *MalformedError) Error() string { return e.Code + ": " + e.Reason }

var moveRe = regexp.MustCompile(`^([a-h][1-8][a-h][1-8][qrbn]?)( claim)?$`)

// ParseAction checks the action grammar and the talk/memo limits.
func ParseAction(action, talk, memo string) (Action, error) {
	if n := utf8.RuneCountInString(talk); n > MaxTalkRunes {
		return Action{}, &MalformedError{"talk_too_long", fmt.Sprintf("talk is %d characters; the limit is %d", n, MaxTalkRunes)}
	}
	if n := utf8.RuneCountInString(memo); n > MaxMemoRunes {
		return Action{}, &MalformedError{"memo_too_long", fmt.Sprintf("memo is %d characters; the limit is %d", n, MaxMemoRunes)}
	}
	s := strings.TrimSpace(action)
	a := Action{Talk: talk, Memo: memo}
	switch s {
	case "claim_draw":
		a.Kind = KindClaimDraw
	case "resign":
		a.Kind = KindResign
	case "offer_draw":
		a.Kind = KindOfferDraw
	case "accept_draw":
		a.Kind = KindAcceptDraw
	default:
		m := moveRe.FindStringSubmatch(s)
		if m == nil {
			return Action{}, &MalformedError{"bad_action", fmt.Sprintf("%q is not in the action grammar", s)}
		}
		a.Kind, a.UCI, a.Claim = KindMove, m[1], m[2] != ""
	}
	return a, nil
}
