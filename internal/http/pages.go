package http

import (
	"html/template"
	stdhttp "net/http"
	"path/filepath"
	"sync"
	"time"

	"kisschess/internal/chess"
)

// pageTemplates lazily parses lobby.html and game.html from staticRoot the
// first time they're requested, then caches them for the life of the
// server. Reparse-per-request would be wasteful given these are static
// shells; Datastar drives all the dynamism over SSE.
type pageTemplates struct {
	once  sync.Once
	lobby *template.Template
	game  *template.Template
	err   error
}

var pages pageTemplates

func (s *Server) loadPages() (*template.Template, *template.Template, error) {
	pages.once.Do(func() {
		lobby, err := template.ParseFiles(filepath.Join(s.staticRoot, "lobby.html"))
		if err != nil {
			pages.err = err
			return
		}
		game, err := template.ParseFiles(filepath.Join(s.staticRoot, "game.html"))
		if err != nil {
			pages.err = err
			return
		}
		pages.lobby = lobby
		pages.game = game
	})
	return pages.lobby, pages.game, pages.err
}

// handleLobby renders web/lobby.html as the index page.
func (s *Server) handleLobby(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	lobby, _, err := s.loadPages()
	if err != nil {
		stdhttp.Error(w, "template error: "+err.Error(), stdhttp.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = lobby.Execute(w, nil)
}

// gamePageData is the template context for game.html. The snapshot is
// taken at render time so the initial Datastar signal store is hydrated
// with the latest state; subsequent updates arrive via SSE.
//
// White and Black expose the participants so the template can render a
// share-with-opponent link. MyColor is the server-computed "which side
// is this viewer on" — "w", "b", or "" for spectator — used to drive
// board orientation and the draw-offer banner without needing client-
// side string compares.
type gamePageData struct {
	GameID   chess.GameID
	As       chess.PlayerID
	White    chess.PlayerID
	Black    chess.PlayerID
	MyColor  string // "w" | "b" | "" (spectator)
	Snapshot chess.BoardSnapshot
}

// handleGamePage renders web/game.html for GET /game/{id}.
//
// Query string `?as=<player_id>` identifies which side the user is on.
// Spectators omit the parameter — the form submits with X-Player-ID equal
// to whatever `as` was given (empty for pure spectators, who can't move).
func (s *Server) handleGamePage(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	_, game, err := s.loadPages()
	if err != nil {
		stdhttp.Error(w, "template error: "+err.Error(), stdhttp.StatusInternalServerError)
		return
	}
	id := chess.GameID(r.PathValue("id"))
	room, ok := s.Manager.Get(id)
	if !ok {
		stdhttp.NotFound(w, r)
		return
	}

	white, black := room.Participants()
	as := chess.PlayerID(r.URL.Query().Get("as"))
	var myColor string
	switch as {
	case white:
		myColor = "w"
	case black:
		myColor = "b"
	default:
		myColor = "" // spectator
	}
	data := gamePageData{
		GameID:   id,
		As:       as,
		White:    white,
		Black:    black,
		MyColor:  myColor,
		Snapshot: room.Snapshot(time.Now()),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = game.Execute(w, data)
}
