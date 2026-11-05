package arena

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Snapshot is the subset of the server's BoardSnapshot the arena reads.
type Snapshot struct {
	Seq           uint64   `json:"seq"`
	Turn          uint8    `json:"turn"` // 0 = White, 1 = Black
	Outcome       uint8    `json:"outcome"`
	Termination   uint8    `json:"termination"`
	LastMove      string   `json:"last_move"`
	FEN           string   `json:"fen"`
	DrawOfferFrom string   `json:"draw_offer_from"` // "w" | "b" | ""
	LegalMoves    []string `json:"legal_moves"`
	CanClaimDraw  bool     `json:"can_claim_draw"`
}

// Server outcome and termination codes (internal/chess/types.go).
const (
	outcomeOngoing  = 0
	outcomeWhiteWin = 1
	outcomeBlackWin = 2
	outcomeDraw     = 3
)

var terminationNames = []string{"", "checkmate", "stalemate", "insufficient_material", "fivefold_repetition",
	"seventy_five_move_rule", "threefold_claim", "fifty_move_claim", "flag", "resign", "agreed_draw"}

// ServerError is a non-2xx answer from the chess server, or a transport
// failure (Status 0).
type ServerError struct {
	Status int
	Code   string
	Msg    string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("chess server %d %s: %s", e.Status, e.Code, e.Msg)
}

// Transient reports whether a retry may succeed (network or 5xx).
func (e *ServerError) Transient() bool { return e.Status == 0 || e.Status >= 500 }

// ServerClient talks to cmd/server over its public JSON endpoints.
type ServerClient struct {
	Base string
	HTTP *http.Client
}

// NewServerClient returns a client for base, e.g. http://localhost:8080.
func NewServerClient(base string) *ServerClient {
	return &ServerClient{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (s *ServerClient) do(ctx context.Context, method, path, player string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if player != "" {
		req.Header.Set("X-Player-ID", player)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ServerError{Code: "network", Msg: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		code := env.Error.Code
		if code == "" {
			code = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		return &ServerError{Status: resp.StatusCode, Code: code, Msg: env.Error.Message}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// CreateGame creates a game and returns its server id.
func (s *ServerClient) CreateGame(ctx context.Context, white, black string, tc TimeControl) (string, error) {
	var out struct {
		GameID string `json:"game_id"`
	}
	err := s.do(ctx, http.MethodPost, "/games", "", map[string]any{
		"white": white, "black": black,
		"time_control": map[string]int64{
			"initial_ms": tc.Initial.Milliseconds(), "increment_ms": tc.Increment.Milliseconds(), "delay_ms": tc.Delay.Milliseconds(),
		},
	}, &out)
	return out.GameID, err
}

// Snapshot fetches the current authoritative state.
func (s *ServerClient) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	var out struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	err := s.do(ctx, http.MethodGet, "/games/"+id+"/snapshot", "", nil, &out)
	return out.Snapshot, err
}

// Move submits a UCI move, optionally with a draw claim.
func (s *ServerClient) Move(ctx context.Context, id, player string, seq uint64, uci string, claim bool, caid string) error {
	return s.do(ctx, http.MethodPost, "/games/"+id+"/move", player,
		map[string]any{"seq": seq, "move": uci, "client_action_id": caid, "claim_draw": claim}, nil)
}

// Act submits resign | draw-offer | draw-offer/accept | claim-draw.
func (s *ServerClient) Act(ctx context.Context, id, player, endpoint string, seq uint64, caid string) error {
	return s.do(ctx, http.MethodPost, "/games/"+id+"/"+endpoint, player,
		map[string]any{"seq": seq, "client_action_id": caid}, nil)
}

func newActionID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
