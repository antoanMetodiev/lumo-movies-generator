# lumo-movies-generator

TMDB movie-ingestion microservice for AuroraFlix — rewritten from Java/Spring
Boot to Go, endpoint-for-endpoint and behavior-for-behavior compatible, to
compare real-world RAM usage on Render's free tier (same motivation as
[lumo-movies-svc](../lumo-movies-svc), the catalog service this one writes
into).

Two endpoints, both synchronous (the caller's request blocks until TMDB has
been fully walked): `POST /search-movies` (raw title in the body) and
`POST /add-movies-by-actor/{imdb_id}`. Everything found gets run through the
same validity checks the Java service used (poster present, sane release
date, non-zero rating, at least 4 images, resolvable cast, non-empty genres)
before being inserted into the shared `movies` table.

## Run locally

```bash
go run ./cmd/server
```

Needs the same environment variables the Java service used: `DATABASE_URL`,
`DATABASE_USERNAME`, `DATABASE_PASSWORD`, `TMDB_API_KEY`, `TMDB_BASE_URL`,
and optionally `GRAFANA_OTLP_ENABLED` / `GRAFANA_OTLP_ENDPOINT` /
`GRAFANA_OTLP_AUTH`. Defaults to port `8888`, same as the Java service's
`server.port`.

## Deploy

`Dockerfile` builds a static binary on a distroless base image — no JDK, no
Gradle. Render picks it up the same way it did the Java Dockerfile.

## Structure

- `cmd/server` — entrypoint, wiring
- `internal/models` — the subset of the shared schema this service writes to
  (Actor, Movie, MovieImage) — see the comment in models.go for how this
  service's Movie columns differ from lumo-movies-svc's richer one
- `internal/movies` — the TMDB-backed generator pipeline (mirrors
  `MoviesGeneratorController`/`MoviesGeneratorService`), plus the trimmed
  `MovieRepository` methods it actually calls
- `internal/actors`, `internal/tmdb`, `internal/dbx`, `internal/metrics`,
  `internal/config`, `internal/reqlog` — supporting pieces, copied from
  lumo-movies-svc's conventions (mostly verbatim — same DB, same TMDB API,
  same Grafana setup)

## Scope note

The Java source in this repo also carried a `MovieCommentRepository`, a
`TrendingMovie` entity, and most of `MovieRepository`'s native queries —
none of it wired into either live endpoint (dead code, likely copy-pasted
from the richer catalog service). None of that was ported; this rewrite only
covers what the two real endpoints actually exercised.
