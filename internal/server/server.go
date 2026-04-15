package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/devpad-org/devpad/internal/admin"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/settings"
	"github.com/devpad-org/devpad/web"
)

// Server is the main application server.
type Server struct {
	httpServer *http.Server
	db         *database.DB
}

// New creates a new Server with the given configuration.
func New(cfg Config) (*Server, error) {
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Migrate(); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Auth layer
	userRepo := auth.NewUserRepository(db.Conn())
	sessionRepo := auth.NewSessionRepository(db.Conn())
	authService := auth.NewService(userRepo, sessionRepo)
	authHandler := auth.NewHandler(authService)
	authMiddleware := auth.NewMiddleware(authService)

	// Admin layer
	adminService := admin.NewService(userRepo)
	adminHandler := admin.NewHandler(adminService)

	// Settings layer
	settingsService := settings.NewService(userRepo)
	settingsHandler := settings.NewHandler(settingsService)

	mux := http.NewServeMux()
	registerRoutes(mux, authHandler, authMiddleware, adminHandler, settingsHandler)

	s := &Server{
		httpServer: &http.Server{
			Addr:    cfg.Addr,
			Handler: mux,
		},
		db: db,
	}

	return s, nil
}

// Start begins listening for HTTP connections.
func (s *Server) Start(ctx context.Context) error {
	log.Printf("server listening on %s", s.httpServer.Addr)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("database close: %w", err)
	}

	return nil
}

// registerRoutes sets up all HTTP routes.
func registerRoutes(mux *http.ServeMux, authHandler *auth.Handler, authMiddleware *auth.Middleware, adminHandler *admin.Handler, settingsHandler *settings.Handler) {
	// Public API routes
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("GET /api/auth/setup", authHandler.HandleSetupCheck)
	mux.HandleFunc("POST /api/auth/setup", authHandler.HandleSetup)
	mux.HandleFunc("POST /api/auth/login", authHandler.HandleLogin)
	mux.HandleFunc("POST /api/auth/logout", authHandler.HandleLogout)

	// Protected API routes
	mux.Handle("GET /api/auth/me", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.HandleMe)))

	// Settings API routes (authenticated users)
	mux.Handle("POST /api/settings/password", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleChangePassword)))
	mux.Handle("GET /api/settings/mfa", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleGetMFAStatus)))
	mux.Handle("POST /api/settings/mfa/setup", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleTOTPSetup)))
	mux.Handle("POST /api/settings/mfa/enable", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleTOTPEnable)))
	mux.Handle("POST /api/settings/mfa/disable", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleTOTPDisable)))

	// Admin API routes
	mux.Handle("GET /api/admin/users", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleListUsers)))
	mux.Handle("POST /api/admin/users", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleCreateUser)))
	mux.Handle("PUT /api/admin/users/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleUpdateUser)))
	mux.Handle("POST /api/admin/users/{id}/reset-password", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleResetPassword)))
	mux.Handle("DELETE /api/admin/users/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleDeleteUser)))

	// Serve embedded frontend for all other routes
	mux.Handle("/", web.Handler())
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}
