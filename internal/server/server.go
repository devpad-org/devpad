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
	"github.com/devpad-org/devpad/internal/gitbridge"
	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/preview"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/projecttools"
	"github.com/devpad-org/devpad/internal/settings"
	"github.com/devpad-org/devpad/internal/task"
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
	taskShutdown   func(context.Context) error
	mrShutdown     func(context.Context) error
	// gitBridgeStop stops polling project workspaces for git requests.
	gitBridgeStop func()
}

// New creates a new Server with the given configuration.
func New(cfg Config) (*Server, error) {
	if err := validateDomains(cfg); err != nil {
		return nil, err
	}

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
	secureCookie := secureCookies(cfg)
	if secureCookie && cfg.Domain == "" {
		log.Printf("--preview-domain is set, so session cookies are Secure (%s): devpad must be served over HTTPS, as previews already require", auth.SessionCookieName(true))
	}
	authHandler := auth.NewHandler(authService, secureCookie)
	authMiddleware := auth.NewMiddleware(authService, secureCookie)

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

	// Workspace services layer (sidecars)
	wsServiceRepo := wsservice.NewRepository(db.Conn())
	wsServiceService := wsservice.NewService(wsServiceRepo, containerManager)
	workspaceService.SetSidecarService(wsServiceService)

	// Admin layer (depends on workspace service for resource limit management)
	adminService := admin.NewService(userRepo, workspaceService)
	adminHandler := admin.NewHandler(adminService)

	origins := appOrigins(cfg)
	workspaceHandler := workspace.NewHandler(workspaceService, origins)
	wsServiceHandler := wsservice.NewHandler(wsServiceService, workspaceService)

	// Project layer (canonical repos and the sandboxes seeded from them)
	projectRepo := project.NewRepository(db.Conn())
	projectService := project.NewService(projectRepo, containerManager, workspaceService, userRepo, cipher, settingsService)
	projectHandler := project.NewHandler(projectService)

	// AI layer
	aiModule := ai.NewModule(db.Conn(), workspaceService)

	// Task layer (agents implementing tasks in project sandboxes)
	taskService := task.NewService(task.NewRepository(db.Conn()), projectService, workspaceService, aiModule.AgentRunService, aiModule.ConversationService, aiModule.AgentDefaults, aiModule.BudgetDefaults)
	aiModule.OnAgentRunFinished(taskService.RunFinished)
	taskHandler := task.NewHandler(taskService)

	// Merge request layer (reviews by users and review agents, and merging)
	mrService := mergerequest.NewService(mergerequest.NewRepository(db.Conn()), projectService, workspaceService, taskService, aiModule.AgentRunService, aiModule.ConversationService, aiModule.AgentDefaults, aiModule.BudgetDefaults)
	taskService.SetReviewHook(mrService)
	projectService.SetPublishHook(mrService)
	aiModule.OnAgentRunFinished(mrService.RunFinished)
	aiModule.AddToolProvider(mrService)
	mrHandler := mergerequest.NewHandler(mrService)

	// git push and fetch through the devpad remote in a project's workspace.
	gitBridge := gitbridge.New(workspaceService, projectService)
	gitBridgeHandler := gitbridge.NewHandler(gitBridge)
	gitBridgeCtx, gitBridgeCancel := context.WithCancel(context.Background())
	gitBridgeDone := make(chan struct{})
	go func() {
		gitBridge.Run(gitBridgeCtx)
		close(gitBridgeDone)
	}()

	// The assistant in a project's workspace sees the board and hands work to
	// task agents.
	aiModule.AddToolProvider(projecttools.NewProvider(workspaceService, projectService, taskService, mrService, aiModule.AgentDefaults))

	if err := projectService.Reconcile(context.Background()); err != nil {
		log.Printf("finishing interrupted workspace conversions: %v", err)
	}
	if err := taskService.Reconcile(context.Background()); err != nil {
		log.Printf("reconciling in-progress tasks: %v", err)
	}
	if err := mrService.Reconcile(context.Background()); err != nil {
		log.Printf("reconciling pending reviews: %v", err)
	}

	// Preview layer
	previewRepo := preview.NewRepository(db.Conn())
	previewService := preview.NewService(previewRepo, workspaceRepo, containerManager)
	previewHandler := preview.NewHandler(previewService, cfg.PreviewDomain, cfg.Port)

	// Auth rate limiter: 5 attempts per second, burst of 10 per IP.
	authRateLimiter := auth.NewRateLimiter(rate.Limit(5), 10)

	mux := http.NewServeMux()
	registerRoutes(mux, authHandler, authMiddleware, authRateLimiter, adminHandler, settingsHandler, workspaceHandler, wsServiceHandler, projectHandler, taskHandler, mrHandler, gitBridgeHandler, aiModule.Handler, previewHandler)

	handler := buildHandler(mux, http.HandlerFunc(previewHandler.HandleProxy), cfg)

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
		taskShutdown:  taskService.Shutdown,
		mrShutdown:    mrService.Shutdown,
		gitBridgeStop: func() {
			gitBridgeCancel()
			<-gitBridgeDone
		},
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
	if s.gitBridgeStop != nil {
		s.gitBridgeStop()
	}
	// Stop the task orchestrator first so runs cancelled by the AI shutdown
	// are left for Reconcile instead of failing their tasks.
	if s.taskShutdown != nil {
		if err := s.taskShutdown(ctx); err != nil {
			return fmt.Errorf("task shutdown: %w", err)
		}
	}
	if s.mrShutdown != nil {
		if err := s.mrShutdown(ctx); err != nil {
			return fmt.Errorf("merge request shutdown: %w", err)
		}
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
	HandleListAgents(http.ResponseWriter, *http.Request)
	HandleCreateAgent(http.ResponseWriter, *http.Request)
	HandleUpdateAgent(http.ResponseWriter, *http.Request)
	HandleDeleteAgent(http.ResponseWriter, *http.Request)
	HandleChat(http.ResponseWriter, *http.Request)
	HandleAgentChat(http.ResponseWriter, *http.Request)
	HandleApproveCommand(http.ResponseWriter, *http.Request)
	HandleAnswerQuestion(http.ResponseWriter, *http.Request)
	HandleCreateAgentRun(http.ResponseWriter, *http.Request)
	HandleListAgentRuns(http.ResponseWriter, *http.Request)
	HandleGetAgentRun(http.ResponseWriter, *http.Request)
	HandleAgentRunEvents(http.ResponseWriter, *http.Request)
	HandleCancelAgentRun(http.ResponseWriter, *http.Request)
	HandleListAttention(http.ResponseWriter, *http.Request)
	HandleListConversations(http.ResponseWriter, *http.Request)
	HandleCreateConversation(http.ResponseWriter, *http.Request)
	HandleDeleteConversation(http.ResponseWriter, *http.Request)
	HandleGetMessages(http.ResponseWriter, *http.Request)
	HandleSaveMessages(http.ResponseWriter, *http.Request)
	HandleListProviders(http.ResponseWriter, *http.Request)
	HandleUpdateProvider(http.ResponseWriter, *http.Request)
	HandleListAgentDefaults(http.ResponseWriter, *http.Request)
	HandleSetAgentDefaults(http.ResponseWriter, *http.Request)
	HandleGetBudgetDefaults(http.ResponseWriter, *http.Request)
	HandleSetBudgetDefaults(http.ResponseWriter, *http.Request)
}

// registerRoutes sets up all HTTP routes.
func registerRoutes(mux *http.ServeMux, authHandler *auth.Handler, authMiddleware *auth.Middleware, authRateLimiter *auth.RateLimiter, adminHandler *admin.Handler, settingsHandler *settings.Handler, workspaceHandler *workspace.Handler, wsServiceHandler *wsservice.Handler, projectHandler *project.Handler, taskHandler *task.Handler, mrHandler *mergerequest.Handler, gitBridgeHandler *gitbridge.Handler, aiHandler aiRouteHandler, previewHandler *preview.Handler) {
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
	mux.Handle("GET /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleGet)))
	mux.Handle("PUT /api/workspaces/{id}/default-agent", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleSetDefaultAgent)))
	mux.Handle("DELETE /api/workspaces/{id}", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleDelete)))
	mux.Handle("POST /api/workspaces/{id}/start", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleStart)))
	mux.Handle("POST /api/workspaces/{id}/stop", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleStop)))
	mux.Handle("POST /api/workspaces/{id}/restart", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleRestart)))
	mux.Handle("GET /api/workspaces/{id}/info", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleInfo)))
	mux.Handle("GET /api/workspaces/{id}/processes", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleListProcesses)))
	mux.Handle("POST /api/workspaces/{id}/processes/{pid}/kill", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleKillProcess)))
	mux.Handle("GET /api/workspaces/{id}/terminal", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleTerminal)))
	mux.Handle("GET /api/workspaces/{id}/watch", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleWatch)))

	// Workspace file operation routes
	mux.Handle("GET /api/workspaces/{id}/files", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleListFiles)))
	mux.Handle("GET /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleReadFile)))
	mux.Handle("PUT /api/workspaces/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleWriteFile)))
	mux.Handle("POST /api/workspaces/{id}/file/upload", authMiddleware.RequireAuth(http.HandlerFunc(workspaceHandler.HandleUploadFile)))
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

	// Workspace service routes (sidecars)
	mux.Handle("GET /api/workspaces/{id}/services", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleList)))
	mux.Handle("POST /api/workspaces/{id}/services", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleCreate)))
	mux.Handle("POST /api/workspaces/{id}/services/{serviceId}/start", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleStart)))
	mux.Handle("POST /api/workspaces/{id}/services/{serviceId}/stop", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleStop)))
	mux.Handle("DELETE /api/workspaces/{id}/services/{serviceId}", authMiddleware.RequireAuth(http.HandlerFunc(wsServiceHandler.HandleDelete)))

	// Project routes
	mux.Handle("GET /api/projects", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleList)))
	mux.Handle("GET /api/projects/overview", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleOverview)))
	mux.Handle("POST /api/projects", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleCreate)))
	mux.Handle("GET /api/projects/{id}", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleGet)))
	mux.Handle("PUT /api/projects/{id}", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleUpdate)))
	mux.Handle("DELETE /api/projects/{id}", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleDelete)))
	mux.Handle("GET /api/projects/{id}/branches", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleBranches)))
	mux.Handle("PUT /api/projects/{id}/branches", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleSetBranches)))
	mux.Handle("GET /api/projects/{id}/sandboxes", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleListSandboxes)))
	mux.Handle("POST /api/projects/{id}/sandboxes", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleCreateSandbox)))
	mux.Handle("POST /api/projects/{id}/sandboxes/{workspaceId}/publish", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandlePublishSandbox)))
	mux.Handle("POST /api/projects/{id}/sandboxes/{workspaceId}/start", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleStartSandbox)))
	mux.Handle("GET /api/projects/{id}/compare", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleCompare)))
	mux.Handle("GET /api/projects/{id}/diff", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleDiff)))
	mux.Handle("GET /api/projects/{id}/file", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleFile)))
	mux.Handle("POST /api/projects/{id}/push", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandlePush)))
	mux.Handle("GET /api/projects/{id}/workspace", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleGetWorkspace)))
	mux.Handle("POST /api/projects/{id}/workspace", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleOpenWorkspace)))
	mux.Handle("POST /api/projects/{id}/workspace/checkout", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleCheckoutWorkspaceBranch)))
	mux.Handle("POST /api/projects/{id}/workspace/publish", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandlePublishWorkspaceBranch)))
	mux.Handle("POST /api/projects/{id}/workspace/sync", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleSyncWorkspace)))
	mux.Handle("GET /api/projects/{id}/workspace/branch-status", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleWorkspaceBranchStatus)))
	mux.Handle("GET /api/projects/{id}/workspace/setup", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleWorkspaceSetup)))
	mux.Handle("POST /api/projects/{id}/workspace/setup", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleRunWorkspaceSetup)))
	mux.Handle("POST /api/projects/{id}/workspace/rebuild", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleRebuildWorkspace)))
	mux.Handle("GET /api/projects/{id}/environment", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleEnvironment)))
	mux.Handle("POST /api/projects/{id}/environment/build", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleBuildEnvironment)))
	mux.Handle("GET /api/projects/{id}/git-activity", authMiddleware.RequireAuth(http.HandlerFunc(gitBridgeHandler.HandleActivity)))
	mux.Handle("GET /api/workspaces/{id}/conversion", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleDiscoverConversion)))
	mux.Handle("POST /api/workspaces/{id}/convert", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleConvertWorkspace)))
	mux.Handle("GET /api/projects/{id}/conversion", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleConversionNotice)))
	mux.Handle("POST /api/projects/{id}/conversion/dismiss", authMiddleware.RequireAuth(http.HandlerFunc(projectHandler.HandleDismissConversionNotice)))
	mux.Handle("GET /api/projects/{id}/tasks", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleList)))
	mux.Handle("POST /api/projects/{id}/tasks", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleCreate)))
	mux.Handle("POST /api/projects/{id}/tasks/handoff", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleHandoff)))
	mux.Handle("GET /api/projects/{id}/tasks/{taskId}", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleGet)))
	mux.Handle("PUT /api/projects/{id}/tasks/{taskId}", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleUpdate)))
	mux.Handle("DELETE /api/projects/{id}/tasks/{taskId}", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleDelete)))
	mux.Handle("POST /api/projects/{id}/tasks/{taskId}/start", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleStart)))
	mux.Handle("POST /api/projects/{id}/tasks/{taskId}/cancel", authMiddleware.RequireAuth(http.HandlerFunc(taskHandler.HandleCancel)))
	mux.Handle("GET /api/projects/{id}/merge-requests", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleList)))
	mux.Handle("POST /api/projects/{id}/merge-requests", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleCreate)))
	mux.Handle("GET /api/projects/{id}/merge-requests/{mrId}", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleGet)))
	mux.Handle("GET /api/projects/{id}/merge-requests/{mrId}/diff", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleDiff)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/reviews", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleReview)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/comments", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleComment)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/comments/{commentId}/resolve", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleResolveThread)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/agent-reviews", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleAgentReview)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/merge", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleMerge)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/close", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleClose)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/resolve-conflicts", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleResolveConflicts)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/send-back", authMiddleware.RequireAuth(http.HandlerFunc(mrHandler.HandleSendBack)))

	// AI API routes
	mux.Handle("GET /api/ai/models", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListModels)))
	mux.Handle("GET /api/ai/agents", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListAgents)))
	mux.Handle("POST /api/ai/agents", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCreateAgent)))
	mux.Handle("PUT /api/ai/agents/{id}", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleUpdateAgent)))
	mux.Handle("DELETE /api/ai/agents/{id}", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleDeleteAgent)))
	mux.Handle("POST /api/ai/chat", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleChat)))
	mux.Handle("POST /api/ai/agent", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleAgentChat)))
	mux.Handle("POST /api/ai/agent/approve", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleApproveCommand)))
	mux.Handle("POST /api/ai/agent/questions/answer", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleAnswerQuestion)))
	mux.Handle("POST /api/ai/agent/runs", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCreateAgentRun)))
	mux.Handle("GET /api/ai/agent/runs", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListAgentRuns)))
	mux.Handle("GET /api/ai/agent/runs/{id}", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleGetAgentRun)))
	mux.Handle("GET /api/ai/agent/runs/{id}/events", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleAgentRunEvents)))
	mux.Handle("POST /api/ai/agent/runs/{id}/cancel", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCancelAgentRun)))
	mux.Handle("GET /api/ai/agent/attention", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListAttention)))

	// AI conversation history routes
	mux.Handle("GET /api/ai/conversations", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListConversations)))
	mux.Handle("POST /api/ai/conversations", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCreateConversation)))
	mux.Handle("DELETE /api/ai/conversations/{id}", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleDeleteConversation)))
	mux.Handle("GET /api/ai/conversations/{id}/messages", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleGetMessages)))
	mux.Handle("PUT /api/ai/conversations/{id}/messages", authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleSaveMessages)))

	// AI admin routes
	mux.Handle("GET /api/ai/providers", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleListProviders)))
	mux.Handle("PUT /api/ai/providers/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleUpdateProvider)))
	mux.Handle("GET /api/ai/agent-defaults", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleListAgentDefaults)))
	mux.Handle("PUT /api/ai/agent-defaults/{id}", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleSetAgentDefaults)))
	mux.Handle("GET /api/ai/budgets", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleGetBudgetDefaults)))
	mux.Handle("PUT /api/ai/budgets", authMiddleware.RequireAdmin(http.HandlerFunc(aiHandler.HandleSetBudgetDefaults)))

	// Unknown API paths get a JSON 404 rather than the SPA, so a client
	// calling a removed or mistyped endpoint sees a real error.
	mux.HandleFunc("/api/", handleAPINotFound)

	// Serve embedded frontend for all other routes
	mux.Handle("/", web.Handler())
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"ok"}`)
}

func handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"error":"not found"}`)
}

// buildHandler wraps the app mux with the same-origin guard and frame
// headers, routes preview subdomain requests to the preview proxy so they
// never reach the app, and applies per-route body size limits to prevent
// memory exhaustion.
func buildHandler(appMux, previewHandler http.Handler, cfg Config) http.Handler {
	app := denyFraming(sameOriginGuard(appMux, appOrigins(cfg)))
	return requestBodyLimit(hostRouter(app, previewHandler, cfg.PreviewDomain))
}

// secureCookies reports whether session cookies must be Secure. A preview
// domain forces it: previews already require HTTPS (their cookie is Secure),
// and only a Secure cookie can carry the __Host- prefix that stops previews
// tossing a session cookie onto the app.
func secureCookies(cfg Config) bool {
	return cfg.Domain != "" || cfg.PreviewDomain != ""
}

// validateDomains refuses a preview domain that would swallow the app's own
// host: the host router sends every host under the preview domain to the
// preview proxy, so the app would be unreachable.
func validateDomains(cfg Config) error {
	if cfg.Domain == "" || cfg.PreviewDomain == "" {
		return nil
	}
	domain := strings.ToLower(strings.TrimSuffix(cfg.Domain, "."))
	preview := strings.ToLower(strings.TrimSuffix(cfg.PreviewDomain, "."))
	if domain == preview || strings.HasSuffix(domain, "."+preview) {
		return fmt.Errorf("--domain %q must not be the preview domain %q or a subdomain of it: every host under the preview domain is served as a workspace preview", cfg.Domain, cfg.PreviewDomain)
	}
	return nil
}

// denyFraming stops other pages, previews included, from framing the app. A
// framed app would load with the user's session and its own requests pass the
// same-origin guard, so a preview could overlay it and trick the user into
// clicking Merge or Approve.
func denyFraming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// hostRouter returns a handler that routes preview subdomain requests to the
// preview proxy and everything else to the main application mux. The preview
// domain itself counts as a preview host, so the app and its API are never
// served on any host under it.
func hostRouter(appMux http.Handler, previewHandler http.Handler, previewDomain string) http.Handler {
	if previewDomain == "" {
		return appMux
	}
	previewDomain = strings.ToLower(strings.TrimSuffix(previewDomain, "."))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		// Strip port if present.
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == previewDomain || strings.HasSuffix(host, "."+previewDomain) {
			previewHandler.ServeHTTP(w, r)
			return
		}
		appMux.ServeHTTP(w, r)
	})
}

// requestBodyLimit applies body size limits: 10MB for conversation message saves,
// 10MB plus multipart overhead for workspace file uploads, and 1MB for all other endpoints.
func requestBodyLimit(next http.Handler) http.Handler {
	const defaultLimit = int64(1 << 20)
	const largeBodyLimit = int64(10 << 20)
	const multipartOverheadLimit = int64(1 << 20)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			limit := defaultLimit
			if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/ai/conversations/") {
				limit = largeBodyLimit
			}
			if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/workspaces/") && strings.HasSuffix(r.URL.Path, "/file/upload") {
				limit = largeBodyLimit + multipartOverheadLimit
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
