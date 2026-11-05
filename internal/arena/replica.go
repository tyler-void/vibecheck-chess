package arena

import (
	libchess "github.com/corentings/chess/v2"
)

// Replica is the harness's local copy of a game, used only to render SAN
// history and to replay action logs. The server stays the authority: the
// seat loop checks Replica.FEN against the server FEN after every move.
type Replica struct {
	g   *libchess.Game
	san []string
}

// NewReplica returns a replica at the standard starting position.
func NewReplica() *Replica { return &Replica{g: libchess.NewGame()} }

// Apply plays one UCI move and records its SAN.
func (r *Replica) Apply(uci string) error {
	if err := r.g.PushNotationMove(uci, libchess.UCINotation{}, nil); err != nil {
		return err
	}
	pos, moves := r.g.Positions(), r.g.Moves()
	n := len(moves)
	r.san = append(r.san, libchess.AlgebraicNotation{}.Encode(pos[n-1], moves[n-1]))
	return nil
}

// FEN returns the current position.
func (r *Replica) FEN() string { return r.g.FEN() }

// Plies returns the number of half-moves played.
func (r *Replica) Plies() int { return len(r.san) }

// SAN returns a copy of the SAN history.
func (r *Replica) SAN() []string { return append([]string(nil), r.san...) }
