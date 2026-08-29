package movies

import (
	"context"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"lumo-movies-generator/internal/actors"
	"lumo-movies-generator/internal/models"
	"lumo-movies-generator/internal/tmdb"
)

// GenerateService mirrors MoviesGeneratorService (the whole "pull a movie's
// full data from TMDB and populate the catalog" pipeline) + the
// commonData.utils.UtilMethods cast-extraction helpers it called.
type GenerateService struct {
	tmdb       *tmdb.Client
	repo       *Repository
	actorsRepo *actors.Repository
}

func NewGenerateService(tmdbClient *tmdb.Client, repo *Repository, actorsRepo *actors.Repository) *GenerateService {
	return &GenerateService{tmdb: tmdbClient, repo: repo, actorsRepo: actorsRepo}
}

// SearchForMovies mirrors MoviesGeneratorService.searchForMovies.
func (g *GenerateService) SearchForMovies(ctx context.Context, movieName string) {
	if strings.TrimSpace(movieName) == "" {
		log.Printf("searchForMovies: blank title, ignoring")
		return
	}
	log.Printf("searchForMovies: ordered %q — querying TMDB", movieName)

	searchURL := g.tmdb.BaseURL + "/3/search/movie?api_key=" + url.QueryEscape(g.tmdb.APIKey) +
		"&query=" + url.QueryEscape(movieName)

	body, status, err := g.tmdb.Get(searchURL)
	if err != nil {
		log.Printf("searchForMovies(%q): TMDB request failed: %v", movieName, err)
		return
	}
	log.Printf("searchForMovies(%q): TMDB responded status=%d", movieName, status)
	g.processMovies(ctx, body, status)
}

// processMovies mirrors MoviesGeneratorService.processMovies: `body` may be
// a TMDB search response ({"results": [...]}) or a single bare movie object
// (from addMoviesByActor's per-credit lookup below) — both are normalized to
// a slice of movie objects the same way the Java code did.
//
// The Java method wraps its *entire* for-loop in one try/catch, so any
// unexpected exception on movie N (a network blip, a JSON shape surprise —
// notably including a Bean Validation failure on save() if a movie's
// trailerURL ever ended up empty, since CinemaRecord.trailerURL carries
// @Size(min=8)) silently aborts every movie after it in that batch. This Go
// rewrite processes each movie independently instead: TrailerURL is left nil
// rather than empty-string (see processOneMovie) so that particular failure
// mode can't happen, and any other per-movie error is logged and skipped
// rather than aborting the rest of the batch — the same judgment call
// lumo-movies-svc's rewrite already made for the identical Java quirk in the
// service it mirrors.
func (g *GenerateService) processMovies(ctx context.Context, body map[string]any, status int) {
	if status != 200 || body == nil {
		log.Printf("processMovies: nothing to process (status=%d, empty body=%v)", status, body == nil)
		return
	}

	var results []any
	if r := tmdb.Arr(body, "results"); r != nil {
		results = r
	} else {
		results = []any{body}
	}
	log.Printf("processMovies: %d TMDB result(s) returned", len(results))

	for _, elem := range results {
		movieJSON := tmdb.AsObj(elem)
		if movieJSON == nil {
			continue
		}
		g.processOneMovie(ctx, movieJSON)
	}
}

func (g *GenerateService) processOneMovie(ctx context.Context, movieJSON map[string]any) {
	movieID := tmdb.Str(movieJSON, "id")
	title := tmdb.Str(movieJSON, "title")
	description := tmdb.Str(movieJSON, "overview")
	releaseDate := tmdb.Str(movieJSON, "release_date")
	backgroundIMG := tmdb.Str(movieJSON, "backdrop_path")
	posterIMG := tmdb.Str(movieJSON, "poster_path")
	movieRating := tmdb.Str(movieJSON, "vote_average")

	if strings.TrimSpace(posterIMG) == "" {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no poster image", title, movieID)
		return
	}
	if strings.TrimSpace(releaseDate) == "" {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no release date", title, movieID)
		return
	}
	parsedDate, err := time.Parse("2006-01-02", releaseDate)
	if err != nil {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): unparseable release date %q", title, movieID, releaseDate)
		return
	}
	if parsedDate.After(time.Now()) {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): not released yet (%s)", title, movieID, releaseDate)
		return
	}
	if parsedDate.Year() < 2000 {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): released before 2000 (%s)", title, movieID, releaseDate)
		return
	}
	if movieRating == "0.0" {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): TMDB rating is 0.0", title, movieID)
		return
	}
	log.Printf("processOneMovie: %q (tmdbId=%s) passed initial checks, fetching cast/images/genres", title, movieID)

	trailerURL := g.getTrailerFromAPI(movieID)

	videoURL := "https://vidsrc.icu/embed/movie/" + movieID
	castURL := g.tmdb.URL("/3/movie/"+movieID+"/credits", "")

	// Same three concurrent TMDB calls as the Java code's three
	// CompletableFutures, joined before deciding whether to keep the movie.
	var wg sync.WaitGroup
	var castActors []models.Actor
	var images []models.MovieImage
	var imagesOK bool
	var genres, tagline string
	var genresOK bool

	wg.Add(3)
	go func() { defer wg.Done(); castActors = g.extractActors(castURL) }()
	go func() { defer wg.Done(); images, imagesOK = g.extractImages(movieID) }()
	go func() { defer wg.Done(); genres, tagline, genresOK = g.extractGenresAndTagline(movieID) }()
	wg.Wait()

	movie := &models.Movie{}
	g.addAllCast(ctx, castActors, movie)
	if len(castActors) == 0 {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no resolvable cast", title, movieID)
		return
	}
	if !imagesOK {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): fewer than 4 images", title, movieID)
		return
	}
	if !genresOK {
		log.Printf("processOneMovie: skipping %q (tmdbId=%s): no genres", title, movieID)
		return
	}

	movie.VideoURL = videoURL
	movie.TmdbID = &movieID
	movie.SearchTag = title
	movie.Title = title
	if description != "" {
		movie.Description = &description
	}
	movie.ReleaseDate = &releaseDate
	movie.BackgroundImgURL = &backgroundIMG
	movie.PosterImgURL = posterIMG
	movie.TmdbRating = &movieRating
	// nil (not "") when no trailer found — see the processMovies doc comment
	// above for why this Go rewrite doesn't reproduce the Java @Size(min=8)
	// batch-abort quirk.
	if trailerURL != "" {
		movie.TrailerURL = &trailerURL
	}
	movie.CreatedAt = time.Now().UTC()
	movie.ImagesList = images
	if genres != "" {
		movie.Genres = genres
	}
	if tagline != "" {
		movie.SpecialText = &tagline
	}

	log.Printf("processOneMovie: %q (tmdbId=%s) ready to save (cast=%d, images=%d, genres=%q)", title, movieID, len(movie.CastList), len(movie.ImagesList), genres)
	g.saveMovie(ctx, videoURL, movie)
}

// getTrailerFromAPI mirrors MoviesGeneratorService.getTrailerFromApiAndAddHimToDB.
func (g *GenerateService) getTrailerFromAPI(movieID string) string {
	body, status, err := g.tmdb.Get(g.tmdb.URL("/3/movie/"+movieID+"/videos", ""))
	if err != nil || status != 200 || body == nil {
		return ""
	}

	for _, v := range tmdb.Arr(body, "results") {
		video := tmdb.AsObj(v)
		if strings.EqualFold(tmdb.Str(video, "type"), "Trailer") && strings.EqualFold(tmdb.Str(video, "site"), "YouTube") {
			return "https://www.youtube.com/watch?v=" + tmdb.Str(video, "key")
		}
	}
	return ""
}

// extractImages mirrors MoviesGeneratorService.extractImagesAsync. ok=false
// means "found images but fewer than 4" (skip this movie); ok=true with a
// non-200/error response means "couldn't check, don't skip" — same
// asymmetry as the Java code.
func (g *GenerateService) extractImages(movieID string) ([]models.MovieImage, bool) {
	body, status, err := g.tmdb.Get(g.tmdb.URL("/3/movie/"+movieID+"/images", ""))
	if err != nil || status != 200 || body == nil {
		return nil, true
	}

	backdrops := g.extractDetailsImages(tmdb.Arr(body, "backdrops"), models.ImageTypeBackdrop, 29)
	posters := g.extractDetailsImages(tmdb.Arr(body, "posters"), models.ImageTypePoster, 8)
	all := append(backdrops, posters...)

	if len(all) < 4 {
		return nil, false
	}
	return all, true
}

// extractDetailsImages mirrors MoviesGeneratorService.extractDetailsImages,
// including its off-by-one: `count++ == limit` is checked *after* the
// current element is already appended, so backdrops actually caps at 30
// items and posters at 9, not 29/8.
func (g *GenerateService) extractDetailsImages(arr []any, imageType models.ImageType, limit int) []models.MovieImage {
	if arr == nil {
		return []models.MovieImage{}
	}

	var out []models.MovieImage
	for i, elem := range arr {
		obj := tmdb.AsObj(elem)
		if obj == nil {
			continue
		}
		filePath, _ := obj["file_path"].(string)
		out = append(out, models.MovieImage{ImageType: imageType, ImageURL: filePath})
		if i == limit {
			break
		}
	}
	return out
}

// extractGenresAndTagline mirrors MoviesGeneratorService.extractGenresAndTaglineAsync
// — genres joined with a trailing comma, no spaces, exactly like the Java
// StringBuilder loop (append(name).append(",")).
func (g *GenerateService) extractGenresAndTagline(movieID string) (genres, tagline string, ok bool) {
	body, status, err := g.tmdb.Get(g.tmdb.URL("/3/movie/"+movieID, ""))
	if err != nil || status != 200 || body == nil {
		return "", "", true
	}

	tagline = tmdb.Str(body, "tagline")
	var sb strings.Builder
	for _, genreElem := range tmdb.Arr(body, "genres") {
		obj := tmdb.AsObj(genreElem)
		sb.WriteString(tmdb.Str(obj, "name"))
		sb.WriteString(",")
	}
	genres = sb.String()

	if genres == "" {
		return "", "", false
	}
	return genres, tagline, true
}

// extractActors mirrors UtilMethods.extractActors + extractDeepActorInfoAsync.
// The Java version calls .get() on each actor's future sequentially inside
// its loop, so despite being @Async it never actually overlaps two TMDB
// calls; this Go rewrite genuinely fetches every cast member's profile
// concurrently instead, same improvement lumo-movies-svc's rewrite already
// made over the equivalent Java method.
func (g *GenerateService) extractActors(castURL string) []models.Actor {
	body, status, err := g.tmdb.Get(castURL)
	if err != nil || status != 200 || body == nil {
		return nil
	}

	castArr := tmdb.Arr(body, "cast")
	results := make([]*models.Actor, len(castArr))

	var wg sync.WaitGroup
	for i, elem := range castArr {
		obj := tmdb.AsObj(elem)
		actorID := tmdb.Str(obj, "id")
		if actorID == "" {
			continue
		}
		wg.Add(1)
		go func(i int, actorID string) {
			defer wg.Done()
			results[i] = g.extractDeepActorInfo(actorID)
		}(i, actorID)
	}
	wg.Wait()

	var out []models.Actor
	for _, a := range results {
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

func (g *GenerateService) extractDeepActorInfo(actorID string) *models.Actor {
	searchURL := g.tmdb.URL("/3/person/"+actorID, "&append_to_response=external_ids")
	body, status, err := g.tmdb.Get(searchURL)
	if err != nil || status != 200 || body == nil {
		return nil
	}

	name := tmdb.Str(body, "name")
	actorImage := tmdb.Str(body, "profile_path")
	biography := tmdb.Str(body, "biography")
	birthday := tmdb.Str(body, "birthday")
	knownFor := tmdb.Str(body, "known_for_department")
	popularity := tmdb.Str(body, "popularity")
	placeOfBirth := tmdb.Str(body, "place_of_birth")
	gender := tmdb.Str(body, "gender")

	externalIDs := tmdb.Obj(body, "external_ids")
	facebook := tmdb.Str(externalIDs, "facebook_id")
	instagram := tmdb.Str(externalIDs, "instagram_id")
	twitter := tmdb.Str(externalIDs, "twitter_id")
	youtube := tmdb.Str(externalIDs, "youtube_id")
	imdbID := tmdb.Str(externalIDs, "imdb_id")

	if strings.TrimSpace(imdbID) == "" {
		return nil
	}

	return &models.Actor{
		NameInRealLife:    name,
		ImageURL:          strOrNil(actorImage),
		Biography:         strOrNil(biography),
		FacebookUsername:  strOrNil(facebook),
		InstagramUsername: strOrNil(instagram),
		TwitterUsername:   strOrNil(twitter),
		YoutubeChannel:    strOrNil(youtube),
		ImdbID:            strOrNil(imdbID),
		Birthday:          strOrNil(birthday),
		KnownFor:          strOrNil(knownFor),
		Popularity:        strOrNil(popularity),
		PlaceOfBirth:      strOrNil(placeOfBirth),
		Gender:            strOrNil(gender),
	}
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// addAllCast mirrors MoviesGeneratorService.addAllCast, resolving each TMDB
// actor against an existing DB row by imdb_id first and appending the
// resolved (or brand-new) actor onto movie.CastList. Capped at 21 cast
// members — the Java `count++ == 20` check runs *after* the 21st actor is
// already added (post-increment compares the pre-increment value), so this
// reproduces that exact off-by-one rather than a clean 20.
func (g *GenerateService) addAllCast(ctx context.Context, allCast []models.Actor, movie *models.Movie) {
	for i, actor := range allCast {
		resolved := actor
		if actor.ImdbID != nil {
			existing, err := g.actorsRepo.FindByIMDBID(ctx, *actor.ImdbID)
			if err == nil && existing != nil {
				resolved = *existing
			}
		}

		movie.CastList = append(movie.CastList, resolved)

		if i == 20 {
			break
		}
	}
}

// saveMovie mirrors MoviesGeneratorService.saveMovie: skip entirely if a
// movie with this video_url already exists; otherwise persist any
// not-yet-managed cast members first (so they get a real id), then the
// movie itself with its images and the resolved cast id links.
func (g *GenerateService) saveMovie(ctx context.Context, videoURL string, movie *models.Movie) {
	existingID, err := g.repo.FindByVideoURL(ctx, videoURL)
	if err != nil {
		log.Printf("saveMovie: findByVideoURL for %q: %v", movie.Title, err)
		return
	}
	if existingID != nil {
		log.Printf("saveMovie: %q already in catalog (id=%s), skipping", movie.Title, existingID)
		return
	}

	resolvedIDs := make([]uuid.UUID, 0, len(movie.CastList))
	newActors := 0
	for i := range movie.CastList {
		actor := &movie.CastList[i]
		if actor.ID != uuid.Nil {
			resolvedIDs = append(resolvedIDs, actor.ID)
			continue
		}
		if err := g.actorsRepo.Save(ctx, actor); err != nil {
			log.Printf("saveMovie: persisting new actor %q for %q: %v", actor.NameInRealLife, movie.Title, err)
			continue
		}
		newActors++
		resolvedIDs = append(resolvedIDs, actor.ID)
	}
	log.Printf("saveMovie: %q — %d cast member(s) resolved (%d newly created), inserting movie", movie.Title, len(resolvedIDs), newActors)

	if err := g.repo.InsertMovie(ctx, movie, resolvedIDs); err != nil {
		log.Printf("saveMovie: insert %q FAILED: %v", movie.Title, err)
		return
	}
	log.Printf("saveMovie: %q added to the catalog", movie.Title)
}

// AddMoviesByActor mirrors MoviesGeneratorService.addMoviesByActor: resolve
// a TMDB person id from an imdb id, then run every one of that person's
// movie credits through the same processOneMovie pipeline as a normal
// search. The Java source hardcodes "https://api.themoviedb.org" here
// instead of using TMDB_BASE_URL like every other call site — reproduced
// verbatim rather than "fixed", since it's otherwise indistinguishable from
// TMDB's real base URL in production.
func (g *GenerateService) AddMoviesByActor(ctx context.Context, imdbID string) {
	log.Printf("addMoviesByActor(%q): resolving TMDB person id", imdbID)
	findURL := "https://api.themoviedb.org/3/find/" + url.QueryEscape(imdbID) +
		"?external_source=imdb_id&api_key=" + url.QueryEscape(g.tmdb.APIKey)

	findBody, status, err := g.tmdb.Get(findURL)
	if err != nil || status != 200 || findBody == nil {
		log.Printf("addMoviesByActor(%q): /find failed (status=%d): %v", imdbID, status, err)
		return
	}

	personResults := tmdb.Arr(findBody, "person_results")
	if len(personResults) == 0 {
		log.Printf("addMoviesByActor(%q): no TMDB person found", imdbID)
		return
	}

	firstPerson := tmdb.AsObj(personResults[0])
	personID := tmdb.Str(firstPerson, "id")
	personName := tmdb.Str(firstPerson, "name")
	log.Printf("addMoviesByActor(%q): resolved %q (tmdbPersonId=%s), fetching movie credits", imdbID, personName, personID)

	creditsURL := "https://api.themoviedb.org/3/person/" + personID + "/movie_credits?api_key=" + url.QueryEscape(g.tmdb.APIKey)
	creditsBody, status, err := g.tmdb.Get(creditsURL)
	if err != nil || status != 200 || creditsBody == nil {
		log.Printf("addMoviesByActor(%q): no movie credits found (status=%d): %v", imdbID, status, err)
		return
	}

	castMovies := tmdb.Arr(creditsBody, "cast")
	if castMovies == nil {
		log.Printf("addMoviesByActor(%q): no cast movies in credits response", imdbID)
		return
	}
	log.Printf("addMoviesByActor(%q): %q has %d movie credit(s), processing each", imdbID, personName, len(castMovies))

	for _, elem := range castMovies {
		movieJSON := tmdb.AsObj(elem)
		if movieJSON == nil {
			continue
		}
		movieID := tmdb.Str(movieJSON, "id")
		movieTitle := tmdb.Str(movieJSON, "title")

		movieURL := g.tmdb.URL("/3/movie/"+movieID, "")
		body, status, err := g.tmdb.Get(movieURL)
		if err != nil || status != 200 {
			log.Printf("addMoviesByActor(%q): fetching credit %q (tmdbId=%s) failed (status=%d): %v", imdbID, movieTitle, movieID, status, err)
			continue
		}
		g.processMovies(ctx, body, status)
	}
}
