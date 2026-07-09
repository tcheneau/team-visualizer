package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/teamviz/team-visualizer/internal/api"
	"github.com/teamviz/team-visualizer/internal/auth"
	"github.com/teamviz/team-visualizer/internal/config"
	"github.com/teamviz/team-visualizer/internal/store"
	"github.com/teamviz/team-visualizer/internal/ws"
	"github.com/teamviz/team-visualizer/web"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("config: listen=%s db=%s", cfg.Listen, cfg.DBPath)

	// Init SQLite store
	db, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()
	log.Println("database: migrations applied")

	// Init auth service
	authSvc := auth.New(cfg, db)

	// Init API router
	hub := ws.NewHub()
	go hub.Run()
	apiRouter := api.New(authSvc, db, hub)

	// Build root router
	root := chi.NewRouter()
	root.Use(middleware.Logger)
	root.Use(middleware.Recoverer)
	root.Use(middleware.RequestID)

	// API routes (JSON)
	root.Route("/api", func(r chi.Router) {
		apiRouter.RegisterRoutes(r)
	})

	// Legacy app at /legacy/
	root.Handle("/legacy", http.RedirectHandler("/legacy/", http.StatusMovedPermanently))
	root.Handle("/legacy/*", http.StripPrefix("/legacy/", http.FileServer(http.Dir("web/legacy"))))

	// SPA frontend (embedded)
	root.Handle("/*", web.SPAHandler())

	// Start server
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      root,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	done := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down…")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		close(done)
	}()

	log.Printf("server: listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}

	<-done
	log.Println("server: stopped")
}