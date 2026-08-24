// Package models mirrors the Java entities this service actually writes:
// commonData.models.entities.Actor and models.entities.Movie/MovieImage
// (plus their CinemaRecord base fields), field-for-field. This service never
// serializes a Movie back out over HTTP (both endpoints return void, same as
// the Java controller) — these types exist purely to shape what gets written
// to the shared "movies"/"actors" tables that lumo-movies-svc reads from.
// Actor keeps its json tags anyway, matching lumo-movies-svc's models.go
// field-for-field for easy comparison; Movie has none since nothing ever
// encodes it.
package models

import (
	"time"

	"github.com/google/uuid"
)

// ImageType mirrors commonData.models.enums.ImageType.
type ImageType string

const (
	ImageTypeBackdrop ImageType = "BACKDROP"
	ImageTypePoster   ImageType = "POSTER"
)

// Actor mirrors commonData.models.entities.Actor. moviesParticipations is
// omitted entirely — it only exists on the Java side to keep the bidirectional
// JPA association in memory before a cascaded save; nothing here reads it back.
type Actor struct {
	ID                uuid.UUID `json:"id"`
	ImageURL          *string   `json:"imageURL"`
	Biography         *string   `json:"biography"`
	FacebookUsername  *string   `json:"facebookUsername"`
	InstagramUsername *string   `json:"instagramUsername"`
	TwitterUsername   *string   `json:"twitterUsername"`
	YoutubeChannel    *string   `json:"youtubeChannel"`
	ImdbID            *string   `json:"imdbId"`
	Birthday          *string   `json:"birthday"`
	KnownFor          *string   `json:"knownFor"`
	PlaceOfBirth      *string   `json:"placeOfBirth"`
	Gender            *string   `json:"gender"`
	Popularity        *string   `json:"popularity"`
	NameInRealLife    string    `json:"nameInRealLife"`
}

// MovieImage mirrors models.entities.MovieImage. The `movie` back reference
// isn't needed here — InsertMovie links images to their movie by id directly.
type MovieImage struct {
	ID        uuid.UUID `json:"id"`
	ImageType ImageType `json:"imageType"`
	ImageURL  string    `json:"imageURL"`
}

// Movie mirrors models.entities.Movie plus its CinemaRecord base class
// fields, flattened. Note this entity's column set is a subset of the richer
// Movie entity lumo-movies-svc reads (no tmdb_id/vidm_player/fmoon_player/
// abs_player/vidme_player/players_checked columns — those belong to a
// different Java service's schema additions and this one never touched
// them), plus one column the *other* service doesn't have: video_urls.
type Movie struct {
	ID uuid.UUID

	// CinemaRecord (base class) fields:
	Title            string
	PosterImgURL     string
	SpecialText      *string
	TrailerURL       *string
	SearchTag        string
	Genres           string
	Description      *string
	ReleaseDate      *string
	TmdbRating       *string
	BackgroundImgURL *string
	CreatedAt        time.Time

	CastList    []Actor
	VideoURL    string
	ImagesList  []MovieImage
	TrailerURLs []string
	VideoURLs   []string
}
