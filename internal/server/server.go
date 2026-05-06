package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/devpad-org/devpad/internal/admin"
	"github.com/devpad-org/devpad/internal/agentbin"
	"github.com/devpad-org/devpad/internal/ai"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/container"
	"github.com/devpad-org/devpad/internal/database"
	"github.com/devpad-org/devpad/internal/dockerfile"
	"github.com/devpad-org/devpad/internal/encrypt"
	"github.com/devpad-org/devpad/internal/preview"
	"github.com/devpad-org/devpad/internal/settings"
	"github.com/devpad-org/devpad/internal/workspace"
	"github.com/devpad-org/devpad/internal/wsservice"
	"github.com/devpad-org/devpad/web"
)

// Server is the main application server.
type Server struct {
	httpServer     *http.Server
	redirectServer *http.Server
	db             *database.DB
	cfg            Config
	cleanupCancel  context.CancelFunc
	aiShutdown     func(context.Context) error
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

	// Encryption cipher for secrets (SSH private keys).
	if cfg.EncryptionKey == "" {
		key, err := encrypt.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("generating encryption key: %w", err)
		}
		cfg.EncryptionKey = key
		log.Printf("WARNING: No encryption key configured. Generated ephemeral key. SSH keys encrypted with this key will be lost on restart. Use 'openssl rand -hex 32' to generate a persistent key and set DEVPAD_ENCRYPTION_KEY.")
	}
	cipher, err := encrypt.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("creating encryption cipher: %w", err)
	}

	// Auth layer
	userRepo := auth.NewUserRepository(db.Conn())
	sessionRepo := auth.NewSessionRepository(db.Conn())
	authService := auth.NewService(userRepo, sessionRepo)
	secureCookie := cfg.Domain != ""
	authHandler := auth.NewHandler(authService, secureCookie)
	authMiddleware := auth.NewMiddleware(authService)

	// Settings layer
	preferencesRepo := settings.NewPreferencesRepository(db.Conn())
	settingsService := settings.NewService(userRepo, preferencesRepo, cipher)
	settingsHandler := settings.NewHandler(settingsService)

	// Workspace layer
	containerManager, err := container.NewManager()
	if err != nil {
		return nil, fmt.Errorf("creating container manager: %w", err)
	}

	// Auto-build the workspace Docker image from the embedded Dockerfile
	// and agent binary. Skips the build if the image is already up to date.
	if err := containerManager.BuildImage(context.Background(), dockerfile.Content, agentbin.Binary, agentbin.Version); err != nil {
		return nil, fmt.Errorf("building workspace image: %w", err)
	}

	workspaceRepo := workspace.NewRepository(db.Conn())
	workspaceService := workspace.NewService(workspaceRepo, containerManager, userRepo, cipher)

	// Workspace services layer (database sidecars)
	wsServiceRepo := wsservice.NewRepository(db.Conn())
	wsServiceService := wsservice.NewService(wsServiceRepo, containerManager)
	workspaceService.SetSidecarService(wsServiceService)

	// Admin layer (depends on workspace service for resource limit management)
	adminService := admin.NewService(userRepo, workspaceService)
	adminHandler := admin.NewHandler(adminService)

	// Build allowed WebSocket origins from configuration.
	var wsOrigins []string
	if cfg.Domain != "" {
		scheme := "https"
		origin := scheme + "://" + cfg.Domain
		if cfg.Port != 443 {
			origin = fmt.Sprintf("%s://%s:%d", scheme, cfg.Domain, cfg.Port)
		}
		wsOrigins = append(wsOrigins, origin)
	} else {
		// Development mode: allow localhost on the configured port.
		wsOrigins = append(wsOrigins,
			fmt.Sprintf("http://localhost:%d", cfg.Port),
			fmt.Sprintf("http://127.0.0.1:%d", cfg.Port),
		)
	}
	workspaceHandler := workspace.NewHandler(workspaceService, wsOrigins)
	wsServiceHandler := wsservice.NewHandler(wsServiceService, workspaceService)

	// AI layer
	aiModule := ai.NewModule(db.Conn(), workspaceService)

	// Preview layer
	previewRepo := preview.NewRepository(db.Conn())
	previewService := preview.NewService(previewRepo, workspaceRepo, containerManager)
	previewHandler := preview.NewHandler(previewService, cfg.PreviewDomain, cfg.Port)

	// Auth rate limiter: 5 attempts per second, burst of 10 per IP.
	authRateLimiter := auth.NewRateLimiter(rate.Limit(5), 10)

	mux := http.NewServeMux()
	registerRoutes(mux, authHandler, authMiddleware, authRateLimiter, adminHandler, settingsHandler, workspaceHandler, wsServiceHandler, aiModule.Handler, previewHandler)

	// Wrap the mux with host-based routing to intercept preview subdomain requests.
	handler := hostRouter(mux, previewHandler, cfg.PreviewDomain)

	// Apply per-route body size limits to prevent memory exhaustion.
	// Conversation message saves get 10MB; everything else gets 1MB.
	handler = requestBodyLimit(handler)

	// Start periodic cleanup of expired sessions and preview tokens.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := sessionRepo.DeleteExpired(cleanupCtx); err != nil {
					log.Printf("session cleanup: %v", err)
				}
				if err := previewRepo.DeleteExpired(cleanupCtx); err != nil {
					log.Printf("preview token cleanup: %v", err)
				}
			case <-cleanupCtx.Done():
				return
			}
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.Port)

	s := &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			IdleTimeout:  5 * time.Minute,
			WriteTimeout: 0, // Disabled — SSE streams require unbounded writes
		},
		db:            db,
		cfg:           cfg,
		cleanupCancel: cleanupCancel,
		aiShutdown:    aiModule.Shutdown,
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
	if s.cleanupCancel != nil {
		s.cleanupCancel()
	}
	if s.aiShutdown != nil {
		if err := s.aiShutdown(ctx); err != nil {
			return fmt.Errorf("AI shutdown: %w", err)
		}
	}

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

type aiRouteHandler interface {
	HandleListModels(http.ResponseWriter, *http.Request)
	HandleChat(http.ResponseWriter, *http.Request)
	HandleAgentChat(http.ResponseWriter, *http.Request)
	HandleApproveCommand(http.ResponseWriter, *http.Request)
	HandleCreateAgentRun(http.ResponseWriter, *http.Request)
	HandleListAgentRuns(http.ResponseWriter, *http.Request)
	HandleGetAgentRun(http.ResponseWriter, *http.Request)
	HandleAgentRunEvents(http.ResponseWriter, *http.Request)
	HandleCancelAgentRun(http.ResponseWriter, *http.Request)
	HandleListConversations(http.ResponseWriter, *http.Request)
	HandleCreateConversation(http.ResponseWriter, *http.Request)
	HandleDeleteConversation(http.ResponseWriter, *http.Request)
	HandleGetMessages(http.ResponseWriter, *http.Request)
	HandleSaveMessages(http.ResponseWriter, *http.Request)
	HandleListProviders(http.ResponseWriter, *http.Request)
	HandleUpdateProvider(http.ResponseWriter, *http.Request)
}

// registerRoutes sets up all HTTP routes.
func registerRoutes(mux *http.ServeMux, authHandler *auth.Handler, authMiddleware *auth.Middleware, authRateLimiter *auth.RateLimiter, adminHandler *admin.Handler, settingsHandler *settings.Handler, workspaceHandler *workspace.Handler, wsServiceHandler *wsservice.Handler, aiHandler aiRouteHandler, previewHandler *preview.Handler) {
	// Public API routes
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("GET /api/auth/setup", authHandler.HandleSetupCheck)
	mux.Handle("POST /api/auth/setup", authRateLimiter.LimitFunc(authHandler.HandleSetup))
	mux.Handle("POST /api/auth/login", authRateLimiter.LimitFunc(authHandler.HandleLogin))
	mux.HandleFunc("POST /api/auth/logout", authHandler.HandleLogout)

	// Protected API routes
	mux.Handle("GET /api/auth/me", authMiddleware.RequireAuth(http.HandlerFunc(authHandler.HandleMe)))

	// Settings API routes (authenticated users)
	mux.Handle("POST /api/settings/password", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleChangePassword)))
	mux.Handle("GET /api/settings/mfa", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleGetMFAStatus)))
	mux.Handle("POST /api/settings/mfa/setup", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleTOTPSetup)))
	mux.Handle("POST /api/settings/mfa/enable", authMiddleware.RequireAuth(authRateLimiter.LimitFunc(settingsHandler.HandleTOTPEnable)))
	mux.Handle("POST /api/settings/mfa/disable", authMiddleware.RequireAuth(authRateLimiter.LimitFunc(settingsHandler.HandleTOTPDisable)))
	mux.Handle("GET /api/settings/preferences", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleGetPreferences)))
	mux.Handle("PUT /api/settings/preferences", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleUpdatePreferences)))

	// SSH key routes (authenticated users)
	mux.Handle("GET /api/settings/ssh-key", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleGetSSHKey)))
	mux.Handle("POST /api/settings/ssh-key/generate", authMiddleware.RequireAuth(http.HandlerFunc(settingsHandler.HandleGenerateSSHKey)))

	// Admin API routes
	mux.Handle("GET /api/admin/users", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleListUsers)))
	mux.Handle("POST /api/admin/users", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleCreateUser)))
	mux.Handle("PUT /api/admin/users/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleUpdateUser)))
	mux.Handle("POST /api/admin/users/{id}/reset-password", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleResetPassword)))
	mux.Handle("DELETE /api/admin/users/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleDeleteUser)))

	// Admin workspace routes
	mux.Handle("GET /api/admin/workspaces", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleListWorkspaces)))
	mux.Handle("PUT /api/admin/workspaces/{id}/limits", authMiddleware.RequireAdmin(http.HandlerFunc(adminHandler.HandleUpdateWorkspaceLimits)))

	// Workspace API routes
	mux.Handle("GET /api/workspaces", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleList)))
	mux.Handle("POST /api/workspaces", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleCreate)))
	mux.Handle("GET /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGet)))
	mux.Handle("PUT /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleUpdate)))
	mux.Handle("DELETE /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleDelete)))
	mux.Handle("POST /api/workspaces/{id}/start", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleStart)))
	mux.Handle("POST /api/workspaces/{id}/stop", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleStop)))
	mux.Handle("GET /api/workspaces/{id}/info", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleInfo)))
	mux.Handle("GET /api/workspaces/{id}/terminal", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleTerminal)))
	mux.Handle("GET /api/workspaces/{id}/watch", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleWatch)))

	// Workspace file operation routes
	mux.Handle("GET /api/workspaces/{id}/files", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleListFiles)))
	mux.Handle("GET /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleReadFile)))
	mux.Handle("PUT /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleWriteFile)))
	mux.Handle("DELETE /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleDeleteFile)))
	mux.Handle("POST /api/workspaces/{id}/file/mkdir", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleMkdir)))
	mux.Handle("POST /api/workspaces/{id}/file/rename", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleRename)))

	// Workspace git routes
	mux.Handle("GET /api/workspaces/{id}/git/status", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitStatus)))
	mux.Handle("GET /api/workspaces/{id}/git/commits", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitLog)))
	mux.Handle("GET /api/workspaces/{id}/git/commit-files", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitCommitFiles)))
	mux.Handle("GET /api/workspaces/{id}/git/commit-diff", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitCommitFileDiff)))
	mux.Handle("GET /api/workspaces/{id}/git/branches", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitBranches)))
	mux.Handle("GET /api/workspaces/{id}/git/diff", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitDiff)))
	mux.Handle("GET /api/workspaces/{id}/git/file-diff", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitFileDiff)))
	mux.Handle("GET /api/workspaces/{id}/git/remotes", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitRemotes)))
	mux.Handle("POST /api/workspaces/{id}/git/action", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGitAction)))

	// Workspace preview route
	mux.Handle("POST /api/workspaces/{id}/preview", authMiddleware.RequireAuth(http.HandlerFunc(previewHandler.HandleGenerateURL)))

	// Workspace service routes (database sidecars)
	mux.Handle("GET /api/workspaces/{id}/services", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleList)))
	mux.Handle("POST /api/workspaces/{id}/services", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleCreate)))
	mux.Handle("POST /api/workspaces/{id}/services/{serviceId}/start", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleStart)))
	mux.Handle("POST /api/workspaces/{id}/services/{serviceId}/stop", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleStop)))
	mux.Handle("DELETE /api/workspaces/{id}/services/{serviceId}", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleDelete)))

	// AI API routes
	mux.Handle("GET /api/ai/models", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListModels)))
	mux.Handle("POST /api/ai/chat", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleChat)))
	mux.Handle("POST /api/ai/agent", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleAgentChat)))
	mux.Handle("POST /api/ai/agent/approve", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleApproveCommand)))
	mux.Handle("POST /api/ai/agent/runs", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCreateAgentRun)))
	mux.Handle("GET /api/ai/agent/runs", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListAgentRuns)))
	mux.Handle("GET /api/ai/agent/runs/{id}", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleGetAgentRun)))
	mux.Handle("GET /api/ai/agent/runs/{id}/events", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleAgentRunEvents)))
	mux.Handle("POST /api/ai/agent/runs/{id}/cancel", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCancelAgentRun)))

	// AI conversation history routes
	mux.Handle("GET /api/ai/conversations", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListConversations)))
	mux.Handle("POST /api/ai/conversations", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCreateConversation)))
	mux.Handle("DELETE /api/ai/conversations/{id}", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleDeleteConversation)))
	mux.Handle("GET /api/ai/conversations/{id}/messages", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleGetMessages)))
	mux.Handle("PUT /api/ai/conversations/{id}/messages", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleSaveMessages)))

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

// requestBodyLimit applies body size limits: 10MB for conversation message saves
// (which can carry large tool histories) and 1MB for all other endpoints.
func requestBodyLimit(next http.Handler) http.Handler {
	const defaultLimit = int64(1 << 20)
	const saveLimit = int64(10 << 20)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			limit := defaultLimit
			if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/ai/conversations/") {
				limit = saveLimit
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
