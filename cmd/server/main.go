// Command server is the Go rewrite of lumo-movies-generator (originally
// Spring Boot). It's a small TMDB-ingestion service: given a movie title
// (POST /search-movies) or an actor's IMDb id (POST /add-movies-by-actor/{id}),
// it pulls matching movies from TMDB, applies the same validity checks the
// Java service used, and inserts anything that passes into the shared
// "movies" table lumo-movies-svc reads from. Endpoint paths, params, and
// status codes mirror the Java controller exactly — see
// internal/movies/generate.go for where a Java-only framework accident (a
// Bean Validation constraint that could silently truncate an ingest batch)
// was deliberately not reproduced, and why.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lumo-movies-generator/internal/actors"
	"lumo-movies-generator/internal/config"
	"lumo-movies-generator/internal/dbx"
	"lumo-movies-generator/internal/metrics"
	"lumo-movies-generator/internal/movies"
	"lumo-movies-generator/internal/reqlog"
	"lumo-movies-generator/internal/tmdb"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := dbx.Dial(ctx, cfg.DatabaseURL, cfg.DBUsername, cfg.DBPassword)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()

	dbx.StartKeepAlivePing(ctx, pool, log.Printf)

	shutdownMetrics, err := metrics.Start(ctx, cfg.GrafanaEnabled, cfg.GrafanaEndpoint, cfg.GrafanaAuth)
	if err != nil {
		log.Printf("metrics exporter did not start: %v", err)
		shutdownMetrics = func(context.Context) error { return nil }
	}
	defer shutdownMetrics(context.Background())

	tmdbClient := tmdb.New(cfg.TMDBBaseURL, cfg.TMDBAPIKey)

	actorsRepo := actors.NewRepository(pool)
	moviesRepo := movies.NewRepository(pool)
	generateSvc := movies.NewGenerateService(tmdbClient, moviesRepo, actorsRepo)
	moviesHandlers := movies.NewHandlers(generateSvc)

	mux := http.NewServeMux()
	moviesHandlers.Register(mux)
	// management.endpoints.web.exposure.include=health — Render's own health
	// check and any uptime monitor hit this.
	mux.HandleFunc("GET /actuator/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"UP"}`))
	})
	// Plain, non-legacy-named alias of the endpoint above -- this is what
	// lumo-api-gateway's heartbeat.StartFleetHealthPing pings on its own
	// 2-minute schedule to keep every downstream service warm on Render's
	// free tier, independent of an end user ever hitting this service.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"UP"}`))
	})

	handler := reqlog.Middleware(mux)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("lumo-movies-generator (Go) listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
