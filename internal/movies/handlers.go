package movies

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

// Handlers mirrors MoviesGeneratorController — same two paths, same
// synchronous, error-swallowing semantics: the Java controller calls
// straight through to the service and only try/catch-and-logs the result,
// it never dispatches to a background thread, so neither Go handler here
// spawns a goroutine either (unlike lumo-movies-svc's fire-and-forget
// rewrite of a similarly-named endpoint on a *different* Java controller
// that did do that).
type Handlers struct {
	gen *GenerateService
}

func NewHandlers(gen *GenerateService) *Handlers {
	return &Handlers{gen: gen}
}

// Register wires both routes onto mux — mirrors the two @PostMapping
// annotations exactly, path for path.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /add-movies-by-actor/{imdb_id}", h.addMoviesByActor)
	mux.HandleFunc("POST /search-movies", h.searchMovies)
}

// addMoviesByActor mirrors MoviesGeneratorController.addMoviesByActor: runs
// synchronously, catches (recovers from) any failure and just logs it —
// the caller always gets a 200 once the whole pipeline has run.
func (h *Handlers) addMoviesByActor(w http.ResponseWriter, r *http.Request) {
	imdbID := r.PathValue("imdb_id")
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("addMoviesByActor(%q) panic: %v", imdbID, rec)
		}
	}()
	h.gen.AddMoviesByActor(r.Context(), imdbID)
}

// searchMovies mirrors MoviesGeneratorController.searchMovies — reads the
// raw request body as the search title. Spring's @RequestBody String read
// the body as plain text; a caller sending a JSON-quoted string body
// ("Inception") is unwrapped too, so either request shape works the same
// way it would have against the Java endpoint.
func (h *Handlers) searchMovies(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(string(body))
	if len(title) >= 2 && title[0] == '"' && title[len(title)-1] == '"' {
		var unquoted string
		if err := json.Unmarshal([]byte(title), &unquoted); err == nil {
			title = unquoted
		}
	}

	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("searchMovies(%q) panic: %v", title, rec)
		}
	}()
	h.gen.SearchForMovies(r.Context(), title)
}
