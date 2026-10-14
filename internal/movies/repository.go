// Package movies mirrors repositories.MovieRepository + services.
// MoviesGeneratorService — trimmed to the one repository method this
// service's ingest pipeline actually calls (findByVideoURL) plus the insert
// it needs to persist a newly-generated movie. MovieRepository in the Java
// source declares a dozen more native-query methods (findByTitle, discover,
// getNext10Comments, ...) but none of them are ever invoked anywhere in this
// codebase — this service only ever writes movies, it has no endpoint that
// reads them back — so they weren't ported.
package movies

import (
	"context"
	"errors"
	"lumo-movies-generator/internal/enrich"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lumo-movies-generator/internal/models"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// FindByVideoURL mirrors MovieRepository.findByVideoURL — the only place
// that reads its result (saveMovie) only ever checks presence/absence, so
// this returns just the id rather than scanning the full row.
func (r *Repository) FindByVideoURL(ctx context.Context, videoURL string) (*uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, "SELECT id FROM movies WHERE video_url = $1", videoURL).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &id, nil
}

// encodeStringList mirrors StringListConverter.convertToDatabaseColumn: "{}"
// for a nil/empty list, otherwise "{a,b,c}" — no escaping, matching the Java
// converter's own limitation exactly. Neither trailer_urls nor video_urls is
// ever populated by processOneMovie (the Java source doesn't set them
// either), so every insert writes "{}" for both, same as an uninitialized
// Hibernate entity field would.
func encodeStringList(list []string) string {
	if len(list) == 0 {
		return "{}"
	}
	return "{" + strings.Join(list, ",") + "}"
}

// InsertMovie mirrors MoviesGeneratorService.saveMovie + Movie.addAllImages:
// one movie row, its images, and the movies_actors links to already-managed
// actor ids. Runs in a transaction since it's several statements that must
// land together (Hibernate did this implicitly via a single cascaded
// EntityManager.persist()).
func (r *Repository) InsertMovie(ctx context.Context, m *models.Movie, castActorIDs []uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO movies (id, version, title, poster_img_url, trailer_url, special_text,
			search_tag, genres, description, release_date, tmdb_rating, background_img_url, created_at,
			video_url, tmdb_id, trailer_urls, video_urls)
		VALUES ($1,0,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		m.ID, m.Title, m.PosterImgURL, m.TrailerURL, m.SpecialText,
		m.SearchTag, m.Genres, m.Description, m.ReleaseDate, m.TmdbRating, m.BackgroundImgURL, m.CreatedAt,
		m.VideoURL, m.TmdbID, encodeStringList(m.TrailerURLs), encodeStringList(m.VideoURLs),
	)
	if err != nil {
		return err
	}

	for _, img := range m.ImagesList {
		if img.ID == uuid.Nil {
			img.ID = uuid.New()
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO movies_images (id, version, image_type, image_url, movie_id) VALUES ($1,0,$2,$3,$4)`,
			img.ID, img.ImageType, img.ImageURL, m.ID)
		if err != nil {
			return err
		}
	}

	for _, actorID := range castActorIDs {
		_, err = tx.Exec(ctx,
			`INSERT INTO movies_actors (movie_id, actor_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			m.ID, actorID)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Translations, artwork and facts for the new title — in the background
	// (internal/enrich); the sync in tools/translations-backfill catches misses.
	if m.TmdbID != nil {
		enrich.Title(r.pool, "movie", *m.TmdbID)
	}
	return nil
}
