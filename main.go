package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
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

const legacyDir = "web/legacy"

func main() {
	configPath := flag.String("config", "", "path to the TOML configuration file (default: ./teamviz.toml)")
	flag.Parse()

	path := *configPath
	if path == "" {
		path = config.DefaultConfigFile
	}

	// Load config
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("config: loaded %s (listeners=%d)", path, len(cfg.Listeners))

	// Init SQLite store (shared by every listener)
	db, err := store.New(cfg.Server.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()
	log.Println("database: migrations applied")

	// One WebSocket hub shared by all listeners
	hub := ws.NewHub()
	go hub.Run()

	// One authenticated server per listener; each is a full application.
	var servers []*http.Server
	for _, ln := range cfg.Listeners {
		authSvc := auth.New(ln, cfg.Server, db)
		servers = append(servers, &http.Server{
			Addr:         ln.Listen,
			Handler:      buildRouter(authSvc, db, hub),
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		})
	}

	// Serve every listener
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func(s *http.Server) {
			defer wg.Done()
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server: %v", err) // e.g. a port already in use
			}
		}(srv)
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
		for _, s := range servers {
			if err := s.Shutdown(ctx); err != nil {
				log.Printf("shutdown error: %v", err)
			}
		}
		close(done)
	}()

	log.Println("server: listening on", listenerAddresses(servers))
	<-done
	wg.Wait()
	log.Println("server: stopped")
}

func listenerAddresses(servers []*http.Server) string {
	addrs := make([]string, 0, len(servers))
	for _, s := range servers {
		addrs = append(addrs, s.Addr)
	}
	return strings.Join(addrs, ", ")
}

// buildRouter assembles the full application for one listener. The auth
// service (and therefore the auth mode) differs per listener, while the data
// store, WebSocket hub, SPA and legacy files are shared.
func buildRouter(authSvc *auth.AuthService, db *store.Store, hub *ws.Hub) http.Handler {
	apiRouter := api.New(authSvc, db, hub)

	root := chi.NewRouter()
	root.Use(middleware.Logger)
	root.Use(middleware.Recoverer)
	root.Use(middleware.RequestID)

	// Public health check (no auth — used by Docker healthcheck)
	root.Get("/api/health", apiRouter.Health)

	// Public ICS feed (no auth — mounted before the authed /api group)
	root.Get("/api/ics/public/{token}", apiRouter.ServePublicICS)

	// Auth routes (public — OIDC flow handlers; on non-OIDC listeners they
	// answer with a helpful 503 / just clear the session cookie on logout)
	root.Get("/auth/login", authSvc.LoginHandler)
	root.Get("/auth/callback", authSvc.CallbackHandler)
	root.Get("/auth/logout", authSvc.LogoutHandler)

	// API routes (JSON)
	root.Route("/api", func(r chi.Router) {
		apiRouter.RegisterRoutes(r)
	})

	// Legacy app at /legacy/
	root.Handle("/legacy", http.RedirectHandler("/legacy/", http.StatusMovedPermanently))
	root.Handle("/legacy/*", http.StripPrefix("/legacy/", http.FileServer(http.Dir(legacyDir))))

	// SPA frontend (embedded)
	root.Handle("/*", web.SPAHandler())

	return root
}
