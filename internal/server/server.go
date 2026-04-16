package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/devpad-org/devpad/internal/admin"
	"github.com/devpad-org/devpad/internal/ai"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/preview"
	"github.com/devpad-org/devpad/internal/settings"
	"github.com/devpad-org/devpad/internal/workspace"
	"github.com/devpad-org/devpad/web"
)

// Server is the main application server.
type Server struct {
	httpServer     *http.Server
	redirectServer *http.Server
	db             *database.DB
	cfg            Config
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

	// Workspace layer
	containerManager, err := container.NewManager()
	if err != nil {
		return nil, fmt.Errorf("creating container manager: %w", err)
	}
	workspaceRepo := workspace.NewRepository(db.Conn())
	workspaceService := workspace.NewService(workspaceRepo, containerManager)
	workspaceHandler := workspace.NewHandler(workspaceService)

	// AI layer
	aiRepo := ai.NewRepository(db.Conn())
	aiService := ai.NewService(aiRepo, ai.NewMistralProvider(), ai.NewMiniMaxProvider())
	toolExecutor := ai.NewToolExecutor(workspaceService)
	aiHandler := ai.NewHandler(aiService, toolExecutor)

	// Preview layer
	previewRepo := preview.NewRepository(db.Conn())
	previewService := preview.NewService(previewRepo, workspaceRepo, containerManager)
	previewHandler := preview.NewHandler(previewService, cfg.PreviewDomain, cfg.Port)

	mux := http.NewServeMux()
	registerRoutes(mux, authHandler, authMiddleware, adminHandler, settingsHandler, workspaceHandler, aiHandler, previewHandler)

	// Wrap the mux with host-based routing to intercept preview subdomain requests.
	handler := hostRouter(mux, previewHandler, cfg.PreviewDomain)

	addr := fmt.Sprintf(":%d", cfg.Port)

	s := &Server{
		httpServer: &http.Server{
			Addr:    addr,
			Handler: handler,
		},
		db:  db,
		cfg: cfg,
	}

	return s, nil
}

// Start begins listening for connections.
func (s *Server) Start(ctx context.Context) error {
	if s.cfg.Domain != "" {
		return s.startTLS(ctx)
	}
	return s.startPlain()
}

func (s *Server) startPlain() error {
	log.Printf("server listening on %s", s.httpServer.Addr)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	return nil
}

func (s *Server) startTLS(ctx context.Context) error {
	tlsCfg, err := setupTLS(ctx, s.cfg.Domain, s.cfg.PreviewDomain, s.cfg.CFAPIToken)
	if err != nil {
		return fmt.Errorf("setting up TLS: %w", err)
	}
	s.httpServer.TLSConfig = tlsCfg

	log.Printf("server listening on %s (HTTPS, domain=%s)", s.httpServer.Addr, s.cfg.Domain)

	go func() {
		// TLSConfig is already set; pass empty cert/key to use it.
		if err := s.httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	if s.cfg.HTTPRedirectPort > 0 {
		s.startRedirectServer()
	}

	return nil
}

func (s *Server) startRedirectServer() {
	redirectAddr := fmt.Sprintf(":%d", s.cfg.HTTPRedirectPort)
	target := s.cfg.Domain
	if s.cfg.Port != 443 {
		target = fmt.Sprintf("%s:%d", s.cfg.Domain, s.cfg.Port)
	}

	s.redirectServer = &http.Server{
		Addr: redirectAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			url := "https://" + target + r.RequestURI
			http.Redirect(w, r, url, http.StatusMovedPermanently)
		}),
	}

	log.Printf("HTTP redirect server listening on %s → https://%s", redirectAddr, target)

	go func() {
		if err := s.redirectServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("redirect server error: %v", err)
		}
	}()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.redirectServer != nil {
		if err := s.redirectServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("redirect server shutdown: %w", err)
		}
	}

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("database close: %w", err)
	}

	return nil
}

// registerRoutes sets up all HTTP routes.
func registerRoutes(mux *http.ServeMux, authHandler *auth.Handler, authMiddleware *auth.Middleware, adminHandler *admin.Handler, settingsHandler *settings.Handler, workspaceHandler *workspace.Handler, aiHandler *ai.Handler, previewHandler *preview.Handler) {
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

	// Workspace API routes
	mux.Handle("GET /api/workspaces", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleList)))
	mux.Handle("POST /api/workspaces", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleCreate)))
	mux.Handle("GET /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGet)))
	mux.Handle("PUT /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleUpdate)))
	mux.Handle("DELETE /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleDelete)))
	mux.Handle("GET /api/workspaces/{id}/terminal", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleTerminal)))
	mux.Handle("GET /api/workspaces/{id}/watch", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleWatch)))

	// Workspace file operation routes
	mux.Handle("GET /api/workspaces/{id}/files", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleListFiles)))
	mux.Handle("GET /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleReadFile)))
	mux.Handle("PUT /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleWriteFile)))
	mux.Handle("DELETE /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleDeleteFile)))
	mux.Handle("POST /api/workspaces/{id}/file/mkdir", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleMkdir)))
	mux.Handle("POST /api/workspaces/{id}/file/rename", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleRename)))

	// Workspace preview route
	mux.Handle("POST /api/workspaces/{id}/preview", authMiddleware.RequireAuth(http.HandlerFunc(previewHandler.HandleGenerateURL)))

	// AI API routes
	mux.Handle("GET /api/ai/models", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListModels)))
	mux.Handle("POST /api/ai/chat", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleChat)))
	mux.Handle("POST /api/ai/agent", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleAgentChat)))

	// AI admin routes
	mux.Handle("GET /api/ai/providers", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleListProviders)))
	mux.Handle("PUT /api/ai/providers/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleUpdateProvider)))

	// Serve embedded frontend for all other routes
	mux.Handle("/", web.Handler())
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// hostRouter returns a handler that routes preview subdomain requests to the
// preview proxy and everything else to the main application mux.
func hostRouter(appMux http.Handler, previewHandler *preview.Handler, previewDomain string) http.Handler {
	if previewDomain == "" {
		return appMux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		// Strip port if present.
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		if strings.HasSuffix(host, "."+previewDomain) {
			previewHandler.HandleProxy(w, r)
			return
		}
		appMux.ServeHTTP(w, r)
	})
}
