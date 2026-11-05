package arena

import (
	"errors"
	"strings"
	"testing"
)

func TestParseActionGrammar(t *testing.T) {
	cases := []struct {
		in   string
		want Action
	}{
		{"e2e4", Action{Kind: KindMove, UCI: "e2e4"}},
		{"  e7e8q ", Action{Kind: KindMove, UCI: "e7e8q"}},
		{"e2e4 claim", Action{Kind: KindMove, UCI: "e2e4", Claim: true}},
		{"g1f3 claim", Action{Kind: KindMove, UCI: "g1f3", Claim: true}},
		{"claim_draw", Action{Kind: KindClaimDraw}},
		{"resign", Action{Kind: KindResign}},
		{"offer_draw", Action{Kind: KindOfferDraw}},
		{"accept_draw", Action{Kind: KindAcceptDraw}},
	}
	for _, c := range cases {
		got, err := ParseAction(c.in, "", "")
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got.Kind != c.want.Kind || got.UCI != c.want.UCI || got.Claim != c.want.Claim {
			t.Fatalf("%q: got %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestParseActionMalformed(t *testing.T) {
	bad := []string{"", "E2E4", "e2e9", "e2e4k", "e2-e4", "Nf3", "e2e4  claim", "e2e4 Claim",
		"claim", "resign now", "draw", "e2e4\nresign", "offer_draw e2e4"}
	for _, in := range bad {
		_, err := ParseAction(in, "", "")
		var me *MalformedError
		if !errors.As(err, &me) || me.Code != "bad_action" {
			t.Fatalf("%q: want bad_action, got %v", in, err)
		}
	}
}

func TestParseActionLimitsInRunes(t *testing.T) {
	ok140 := strings.Repeat("é", 140)
	if _, err := ParseAction("e2e4", ok140, strings.Repeat("ß", 280)); err != nil {
		t.Fatalf("140/280 runes must pass: %v", err)
	}
	_, err := ParseAction("e2e4", strings.Repeat("a", 141), "")
	var me *MalformedError
	if !errors.As(err, &me) || me.Code != "talk_too_long" {
		t.Fatalf("want talk_too_long, got %v", err)
	}
	_, err = ParseAction("e2e4", "", strings.Repeat("a", 281))
	if !errors.As(err, &me) || me.Code != "memo_too_long" {
		t.Fatalf("want memo_too_long, got %v", err)
	}
}

func TestParseActionKeepsTalkAndMemo(t *testing.T) {
	a, err := ParseAction("resign", "gg", "note")
	if err != nil || a.Talk != "gg" || a.Memo != "note" {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestActionString(t *testing.T) {
	for _, s := range []string{"e2e4", "e2e4 claim", "claim_draw", "resign", "offer_draw", "accept_draw"} {
		a, _ := ParseAction(s, "", "")
		if a.String() != s {
			t.Fatalf("round trip %q -> %q", s, a.String())
		}
	}
}
