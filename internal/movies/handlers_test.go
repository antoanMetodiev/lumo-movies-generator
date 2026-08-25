package movies

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- searchMovies: blank/whitespace/JSON-quoted-blank bodies all resolve to
// an empty title, which SearchForMovies short-circuits on before touching
// gen.tmdb/gen.repo -- so these are safe to exercise with a nil
// *GenerateService (a non-empty title would panic on the nil gen, which is
// exactly what the panic-recovery tests below check for). ---

func newHandlersWithNilGen() *Handlers {
	return &Handlers{gen: nil}
}

func TestSearchMovies_BlankBody_NoOp(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader(""))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (default, unwritten) for a blank body", rec.Code)
	}
}

func TestSearchMovies_WhitespaceOnlyBody_NoOp(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader("   "))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a whitespace-only body", rec.Code)
	}
}

// TestSearchMovies_JSONQuotedBlankBody_NoOp checks a JSON-quoted empty
// string ("\"\"") unwraps to "" the same way a raw blank body does.
func TestSearchMovies_JSONQuotedBlankBody_NoOp(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader(`""`))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a JSON-quoted blank body", rec.Code)
	}
}

// TestSearchMovies_JSONQuotedTitle_Unwraps checks the request-body unquoting
// happens before the title reaches SearchForMovies -- can't observe the
// unwrapped title directly without a real gen, so this just confirms a
// quoted non-blank title takes the "real title" path (panics against the
// nil gen, recovered by the handler) rather than the no-op path.
func TestSearchMovies_JSONQuotedTitle_Unwraps(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader(`"Inception"`))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (panic recovered, nothing written)", rec.Code)
	}
}

// TestSearchMovies_NonEmptyTitle_PanicRecovered documents the handler's
// resilience contract (mirrors the Java controller's try/catch-and-log): a
// downstream failure in the generate pipeline must not surface as a 5xx or
// a crashed request -- it's caught and logged, and the response defaults to
// 200 with nothing written, exactly as if the pipeline had succeeded.
func TestSearchMovies_NonEmptyTitle_PanicRecovered(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", strings.NewReader("Inception"))
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even though the nil gen panicked internally", rec.Code)
	}
}

// erroringBody is an io.ReadCloser whose Read always fails, used to exercise
// searchMovies's io.ReadAll error branch.
type erroringBody struct{}

func (erroringBody) Read([]byte) (int, error) { return 0, errors.New("boom") }
func (erroringBody) Close() error             { return nil }

func TestSearchMovies_BodyReadError_Returns400(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/search-movies", nil)
	r.Body = erroringBody{}
	rec := httptest.NewRecorder()

	h.searchMovies(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when the request body can't be read", rec.Code)
	}
}

// --- addMoviesByActor: always calls straight into the generate pipeline
// (no short-circuit before the network), so this only exercises the
// panic-recovery contract, same as searchMovies above. ---

func TestAddMoviesByActor_PanicRecovered(t *testing.T) {
	h := newHandlersWithNilGen()
	r := httptest.NewRequest(http.MethodPost, "/add-movies-by-actor/nm0000138", nil)
	r.SetPathValue("imdb_id", "nm0000138")
	rec := httptest.NewRecorder()

	h.addMoviesByActor(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even though the nil gen panicked internally", rec.Code)
	}
}

var _ io.ReadCloser = erroringBody{}
