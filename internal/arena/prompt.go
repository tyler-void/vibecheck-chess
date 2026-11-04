package arena

import (
	"embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

//go:embed prompts
var promptFS embed.FS

// ToolDef is the `act` tool as defined by a prompt pack.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Pack is a versioned prompt pack, e.g. "chess/v1".
type Pack struct {
	Version  string
	Tool     ToolDef
	ToolJSON string
	system   *template.Template
	user     *template.Template
}

var tmplFuncs = template.FuncMap{"join": strings.Join}

// LoadPack loads an embedded prompt pack by name.
func LoadPack(name string) (*Pack, error) {
	dir := "prompts/" + name
	sys, err := promptFS.ReadFile(dir + "/system.md")
	if err != nil {
		return nil, fmt.Errorf("arena: prompt pack %q: %w", name, err)
	}
	usr, err := promptFS.ReadFile(dir + "/user.tmpl")
	if err != nil {
		return nil, fmt.Errorf("arena: prompt pack %q: %w", name, err)
	}
	tool, err := promptFS.ReadFile(dir + "/tool.json")
	if err != nil {
		return nil, fmt.Errorf("arena: prompt pack %q: %w", name, err)
	}
	p := &Pack{Version: name, ToolJSON: string(tool)}
	if err := json.Unmarshal(tool, &p.Tool); err != nil {
		return nil, fmt.Errorf("arena: prompt pack %q tool.json: %w", name, err)
	}
	if p.system, err = template.New("system").Option("missingkey=error").Parse(string(sys)); err != nil {
		return nil, err
	}
	if p.user, err = template.New("user").Funcs(tmplFuncs).Option("missingkey=error").Parse(string(usr)); err != nil {
		return nil, err
	}
	return p, nil
}

// SystemParams are the per-match values in the system prompt. They never
// change within a game, so the prefix stays cacheable.
type SystemParams struct {
	PlyCap         int
	DrawOfferEvery int
	TimeoutSeconds int
}

// System renders the system prompt.
func (p *Pack) System(sp SystemParams) (string, error) {
	var b strings.Builder
	err := p.system.Execute(&b, map[string]any{
		"PlyCap": sp.PlyCap, "DrawOfferEvery": sp.DrawOfferEvery, "TimeoutSeconds": sp.TimeoutSeconds,
		"MaxTalk": MaxTalkRunes, "MaxMemo": MaxMemoRunes,
	})
	return b.String(), err
}

// ViewMode selects which actions a decision allows.
type ViewMode string

const (
	ModeNormal       ViewMode = "normal"
	ModeOfferPending ViewMode = "offer_pending" // opponent offered: accept_draw or a move
	ModeFollowUp     ViewMode = "follow_up"     // after this seat's offer_draw: moves only
)

// SeatView is everything one seat may know at one decision.
type SeatView struct {
	Mode           ViewMode
	Color          string // "White" | "Black"
	FEN            string
	Plies, PlyCap  int
	LegalMoves     []string
	SAN            []string
	CanClaimDraw   bool
	OfferAllowed   bool
	OfferWaitMoves int // own moves until the next offer is allowed, when !OfferAllowed
	OpponentTalk   string
	Memo           string
	Attempts       []string // failed attempts at this decision, already phrased
	AttemptsLeft   int
}

// OtherActions lists the non-move actions the seat may take now.
func (v SeatView) OtherActions() []string {
	switch v.Mode {
	case ModeOfferPending:
		return []string{"accept_draw"}
	case ModeFollowUp:
		return nil
	}
	out := []string{"resign"}
	if v.OfferAllowed {
		out = append(out, "offer_draw")
	}
	if v.CanClaimDraw {
		out = append(out, "claim_draw")
	}
	return out
}

func (v SeatView) drawNotes() []string {
	var n []string
	if v.CanClaimDraw {
		n = append(n, "A draw is claimable in the current position (threefold repetition or 50-move rule).")
	}
	if v.Mode == ModeNormal && !v.OfferAllowed {
		n = append(n, fmt.Sprintf("Your next draw offer is allowed after %d more of your own moves.", v.OfferWaitMoves))
	}
	return n
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// User renders the fresh user message for one decision.
func (p *Pack) User(v SeatView) (string, error) {
	var b strings.Builder
	err := p.user.Execute(&b, map[string]any{
		"Mode": string(v.Mode), "Color": v.Color, "Plies": v.Plies, "PlyCap": v.PlyCap,
		"FEN": v.FEN, "Board": ASCIIBoard(v.FEN), "History": numberSAN(v.SAN),
		"LegalMoves": v.LegalMoves, "OtherActions": v.OtherActions(), "DrawNotes": v.drawNotes(),
		"OpponentTalk": escaper.Replace(v.OpponentTalk), "Memo": escaper.Replace(v.Memo),
		"Attempts": v.Attempts, "AttemptsLeft": v.AttemptsLeft,
	})
	return b.String(), err
}

// ASCIIBoard draws the FEN placement from White's side, with coordinates.
func ASCIIBoard(fen string) string {
	placement, _, _ := strings.Cut(fen, " ")
	ranks := strings.Split(placement, "/")
	lines := []string{"  a b c d e f g h"}
	for i, r := range ranks {
		row := make([]string, 0, 8)
		for _, c := range r {
			if c >= '1' && c <= '8' {
				for range int(c - '0') {
					row = append(row, ".")
				}
				continue
			}
			row = append(row, string(c))
		}
		n := strconv.Itoa(8 - i)
		lines = append(lines, n+" "+strings.Join(row, " ")+" "+n)
	}
	return strings.Join(append(lines, "  a b c d e f g h"), "\n")
}

func numberSAN(san []string) string {
	var b strings.Builder
	for i, m := range san {
		if i > 0 {
			b.WriteByte(' ')
		}
		if i%2 == 0 {
			b.WriteString(strconv.Itoa(i/2+1) + ". ")
		}
		b.WriteString(m)
	}
	return b.String()
}
