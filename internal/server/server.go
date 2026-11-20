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
	"github.com/devpad-org/devpad/internal/docs"
	"github.com/devpad-org/devpad/internal/encrypt"
	"github.com/devpad-org/devpad/internal/gitbridge"
	"github.com/devpad-org/devpad/internal/mergerequest"
	"github.com/devpad-org/devpad/internal/preview"
	"github.com/devpad-org/devpad/internal/project"
	"github.com/devpad-org/devpad/internal/projecttools"
	"github.com/devpad-org/devpad/internal/runscope"
	"github.com/devpad-org/devpad/internal/settings"
	"github.com/devpad-org/devpad/internal/skills"
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

	// Encryption cipher for secrets (SSH private keys and AI provider keys).
	// Devpad never makes one up: secrets sealed with a key that is gone
	// with the process could not be read after a restart.
	cipher, err := newCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}

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
	secureCookie := secureCookies(cfg)
	if secureCookie && cfg.Domain == "" {
		log.Printf("--preview-domain is set, so session cookies are Secure (%s): devpad must be served over HTTPS, as previews already require", auth.SessionCookieName(true))
	}
	authHandler := auth.NewHandler(authService, secureCookie)
	authMiddleware := auth.NewMiddleware(authService, secureCookie)
	inviteService := auth.NewInviteService(auth.NewInviteRepository(db.Conn()), sessionRepo)
	inviteHandler := auth.NewInviteHandler(inviteService, authHandler)

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
	settingsService.SetGitIdentitySyncer(workspaceService)

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
	projectService.SetSidecarServices(wsServiceService)
	workspaceService.SetStartHook(projectService)
	projectServicesHandler := wsservice.NewProjectHandler(wsServiceService, projectService, workspaceService)

	// AI layer
	aiModule := ai.NewModule(db.Conn(), workspaceService, cipher)

	// Task layer (agents implementing tasks in project sandboxes)
	taskService := task.NewService(task.Deps{
		Repo:          task.NewRepository(db.Conn()),
		Projects:      projectService,
		Workspaces:    workspaceService,
		Runs:          aiModule.AgentRunService,
		Conversations: aiModule.ConversationService,
		Defaults:      aiModule.AgentDefaults,
		Budgets:       aiModule.BudgetDefaults,
	})
	aiModule.OnAgentRunFinished(taskService.RunFinished)
	taskHandler := task.NewHandler(taskService)

	// Merge request layer (reviews by users and review agents, and merging)
	mrService := mergerequest.NewService(mergerequest.Deps{
		Repo:          mergerequest.NewRepository(db.Conn()),
		Projects:      projectService,
		Workspaces:    workspaceService,
		Tasks:         taskService,
		Runs:          aiModule.AgentRunService,
		Conversations: aiModule.ConversationService,
		Defaults:      aiModule.AgentDefaults,
		Budgets:       aiModule.BudgetDefaults,
		Models:        aiModule.CatalogService,
	})
	taskService.SetReviewHook(mrService)
	projectService.SetPublishHook(mrService)
	projectService.AddBranchGuard(mrService)
	projectService.AddBranchGuard(taskService)
	aiModule.OnAgentRunFinished(mrService.RunFinished)

	// What each run's workspace is, which every tool provider is told. It is
	// set before any provider is added and before reconciling restarts runs.
	scopes := runscope.New(workspaceService, mrService)
	aiModule.SetScopeResolver(scopes)

	// AGENTS.md and the project's skills, and load_skill. Review agents read
	// them from the branch the change would merge into.
	skillsProvider := skills.NewProvider(workspaceService, projectService)
	skillsHandler := skills.NewHandler(skillsProvider, scopes)
	aiModule.AddToolProvider(skillsProvider)

	aiModule.AddToolProvider(mrService)
	mrHandler := mergerequest.NewHandler(mrService)

	// Which services the run's workspace has and how to reach them.
	aiModule.AddToolProvider(wsservice.NewPromptProvider(wsServiceService))

	// git push and fetch through the devpad remote in a project's workspace.
	gitBridge := gitbridge.New(workspaceService, projectService)
	projectService.SetUpstreamHook(gitBridge)
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

	// devpad's own documentation, for the assistant to answer questions
	// about devpad from.
	aiModule.AddToolProvider(docs.NewProvider(userRepo))

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
	previewService := preview.NewService(previewRepo, workspaceRepo, containerManager, preview.Config{Domain: cfg.PreviewDomain, HTTPSPort: cfg.Port})
	previewHandler := preview.NewHandler(previewService, cfg.PreviewDomain)

	// Auth rate limiter: 5 attempts per second, burst of 10 per IP.
	authRateLimiter := auth.NewRateLimiter(rate.Limit(5), 10)

	mux := http.NewServeMux()
	registerRoutes(mux, routeHandlers{
		auth:            authHandler,
		invites:         inviteHandler,
		authMiddleware:  authMiddleware,
		authRateLimiter: authRateLimiter,
		admin:           adminHandler,
		settings:        settingsHandler,
		workspace:       workspaceHandler,
		wsServices:      wsServiceHandler,
		projectServices: projectServicesHandler,
		project:         projectHandler,
		task:            taskHandler,
		mr:              mrHandler,
		gitBridge:       gitBridgeHandler,
		skills:          skillsHandler,
		ai:              aiModule.Handler,
		preview:         previewHandler,
	})

	handler := buildHandler(mux, http.HandlerFunc(previewHandler.HandleProxy), cfg)

	// Start periodic cleanup of expired sessions, preview tokens and invites.
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
				if err := inviteService.PurgeExpired(cleanupCtx); err != nil {
					log.Printf("invite cleanup: %v", err)
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
	HandleUploadAttachment(http.ResponseWriter, *http.Request)
	HandleGetAttachment(http.ResponseWriter, *http.Request)
	HandleListProviders(http.ResponseWriter, *http.Request)
	HandleSetProviderKey(http.ResponseWriter, *http.Request)
	HandleRemoveProviderKey(http.ResponseWriter, *http.Request)
	HandleListAgentDefaults(http.ResponseWriter, *http.Request)
	HandleSetAgentDefaults(http.ResponseWriter, *http.Request)
	HandleGetBudgetDefaults(http.ResponseWriter, *http.Request)
	HandleSetBudgetDefaults(http.ResponseWriter, *http.Request)
	HandleGetSummaryModel(http.ResponseWriter, *http.Request)
	HandleSetSummaryModel(http.ResponseWriter, *http.Request)
	HandleUsageReport(http.ResponseWriter, *http.Request)
}

// routeHandlers are the handlers registerRoutes serves.
type routeHandlers struct {
	auth            *auth.Handler
	invites         *auth.InviteHandler
	authMiddleware  *auth.Middleware
	authRateLimiter *auth.RateLimiter
	admin           *admin.Handler
	settings        *settings.Handler
	workspace       *workspace.Handler
	wsServices      *wsservice.Handler
	projectServices *wsservice.ProjectHandler
	project         *project.Handler
	task            *task.Handler
	mr              *mergerequest.Handler
	gitBridge       *gitbridge.Handler
	skills          *skills.Handler
	ai              aiRouteHandler
	preview         *preview.Handler
}

// registerRoutes sets up all HTTP routes.
func registerRoutes(mux *http.ServeMux, h routeHandlers) {
	// Public API routes
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("GET /api/auth/setup", h.auth.HandleSetupCheck)
	mux.Handle("POST /api/auth/setup", h.authRateLimiter.LimitFunc(h.auth.HandleSetup))
	mux.Handle("POST /api/auth/login", h.authRateLimiter.LimitFunc(h.auth.HandleLogin))
	mux.Handle("POST /api/auth/invites/check", h.authRateLimiter.LimitFunc(h.invites.HandleCheck))
	mux.Handle("POST /api/auth/invites/accept", h.authRateLimiter.LimitFunc(h.invites.HandleAccept))
	mux.HandleFunc("POST /api/auth/logout", h.auth.HandleLogout)

	// Protected API routes
	mux.Handle("GET /api/auth/me", h.authMiddleware.RequireAuth(http.HandlerFunc(h.auth.HandleMe)))

	// Settings API routes (authenticated users)
	mux.Handle("POST /api/settings/password", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleChangePassword)))
	mux.Handle("GET /api/settings/mfa", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleGetMFAStatus)))
	mux.Handle("POST /api/settings/mfa/setup", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleTOTPSetup)))
	mux.Handle("POST /api/settings/mfa/enable", h.authMiddleware.RequireAuth(h.authRateLimiter.LimitFunc(h.settings.HandleTOTPEnable)))
	mux.Handle("POST /api/settings/mfa/disable", h.authMiddleware.RequireAuth(h.authRateLimiter.LimitFunc(h.settings.HandleTOTPDisable)))
	mux.Handle("GET /api/settings/preferences", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleGetPreferences)))
	mux.Handle("PUT /api/settings/preferences", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleUpdatePreferences)))

	// Git identity and SSH key routes (authenticated users)
	mux.Handle("GET /api/settings/git-identity", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleGetGitIdentity)))
	mux.Handle("PUT /api/settings/git-identity", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleUpdateGitIdentity)))
	mux.Handle("GET /api/settings/ssh-key", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleGetSSHKey)))
	mux.Handle("POST /api/settings/ssh-key/generate", h.authMiddleware.RequireAuth(http.HandlerFunc(h.settings.HandleGenerateSSHKey)))

	// Admin API routes
	mux.Handle("GET /api/admin/users", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleListUsers)))
	mux.Handle("POST /api/admin/users", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleCreateUser)))
	mux.Handle("PUT /api/admin/users/{id}", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleUpdateUser)))
	mux.Handle("POST /api/admin/users/{id}/reset-password", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleResetPassword)))
	mux.Handle("DELETE /api/admin/users/{id}", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleDeleteUser)))
	mux.Handle("GET /api/admin/invites", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.invites.HandleList)))
	mux.Handle("POST /api/admin/invites", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.invites.HandleCreate)))
	mux.Handle("DELETE /api/admin/invites/{id}", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.invites.HandleRevoke)))

	// Admin workspace routes
	mux.Handle("GET /api/admin/workspaces", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleListWorkspaces)))
	mux.Handle("PUT /api/admin/workspaces/{id}/limits", h.authMiddleware.RequireAdmin(http.HandlerFunc(h.admin.HandleUpdateWorkspaceLimits)))

	// Workspace API routes
	mux.Handle("GET /api/workspaces", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleList)))
	mux.Handle("GET /api/workspaces/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGet)))
	mux.Handle("PUT /api/workspaces/{id}/default-agent", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleSetDefaultAgent)))
	mux.Handle("DELETE /api/workspaces/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleDelete)))
	mux.Handle("POST /api/workspaces/{id}/start", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleStart)))
	mux.Handle("POST /api/workspaces/{id}/stop", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleStop)))
	mux.Handle("POST /api/workspaces/{id}/restart", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleRestart)))
	mux.Handle("GET /api/workspaces/{id}/info", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleInfo)))
	mux.Handle("GET /api/workspaces/{id}/skills", h.authMiddleware.RequireAuth(http.HandlerFunc(h.skills.HandleList)))

	// Documentation
	docsHandler := docs.NewHandler()
	mux.Handle("GET /api/docs", h.authMiddleware.RequireAuth(http.HandlerFunc(docsHandler.HandleList)))
	mux.Handle("GET /api/docs/{slug}", h.authMiddleware.RequireAuth(http.HandlerFunc(docsHandler.HandleGet)))
	mux.Handle("GET /api/workspaces/{id}/processes", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleListProcesses)))
	mux.Handle("POST /api/workspaces/{id}/processes/{pid}/kill", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleKillProcess)))
	mux.Handle("GET /api/workspaces/{id}/terminal", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleTerminal)))
	mux.Handle("GET /api/workspaces/{id}/watch", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleWatch)))

	// Workspace file operation routes
	mux.Handle("GET /api/workspaces/{id}/files", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleListFiles)))
	mux.Handle("GET /api/workspaces/{id}/file", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleReadFile)))
	mux.Handle("PUT /api/workspaces/{id}/file", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleWriteFile)))
	mux.Handle("POST /api/workspaces/{id}/file/upload", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleUploadFile)))
	mux.Handle("DELETE /api/workspaces/{id}/file", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleDeleteFile)))
	mux.Handle("POST /api/workspaces/{id}/file/mkdir", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleMkdir)))
	mux.Handle("POST /api/workspaces/{id}/file/rename", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleRename)))

	// Workspace git routes
	mux.Handle("GET /api/workspaces/{id}/git/status", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitStatus)))
	mux.Handle("GET /api/workspaces/{id}/git/commits", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitLog)))
	mux.Handle("GET /api/workspaces/{id}/git/commit-files", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitCommitFiles)))
	mux.Handle("GET /api/workspaces/{id}/git/commit-diff", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitCommitFileDiff)))
	mux.Handle("GET /api/workspaces/{id}/git/branches", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitBranches)))
	mux.Handle("GET /api/workspaces/{id}/git/diff", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitDiff)))
	mux.Handle("GET /api/workspaces/{id}/git/file-diff", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitFileDiff)))
	mux.Handle("GET /api/workspaces/{id}/git/remotes", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitRemotes)))
	mux.Handle("POST /api/workspaces/{id}/git/action", h.authMiddleware.RequireAuth(http.HandlerFunc(h.workspace.HandleGitAction)))

	// Workspace preview route
	mux.Handle("POST /api/workspaces/{id}/preview", h.authMiddleware.RequireAuth(http.HandlerFunc(h.preview.HandleGenerateURL)))

	// Workspace service routes (sidecars)
	mux.Handle("GET /api/workspaces/{id}/services", h.authMiddleware.RequireAuth(http.HandlerFunc(h.wsServices.HandleList)))
	mux.Handle("POST /api/workspaces/{id}/services", h.authMiddleware.RequireAuth(http.HandlerFunc(h.wsServices.HandleCreate)))
	mux.Handle("POST /api/workspaces/{id}/services/{serviceId}/start", h.authMiddleware.RequireAuth(http.HandlerFunc(h.wsServices.HandleStart)))
	mux.Handle("POST /api/workspaces/{id}/services/{serviceId}/stop", h.authMiddleware.RequireAuth(http.HandlerFunc(h.wsServices.HandleStop)))
	mux.Handle("DELETE /api/workspaces/{id}/services/{serviceId}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.wsServices.HandleDelete)))

	// Project routes
	mux.Handle("GET /api/projects", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleList)))
	mux.Handle("GET /api/projects/overview", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleOverview)))
	mux.Handle("POST /api/projects", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleCreate)))
	mux.Handle("GET /api/projects/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleGet)))
	mux.Handle("PUT /api/projects/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleUpdate)))
	mux.Handle("DELETE /api/projects/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleDelete)))
	mux.Handle("GET /api/projects/{id}/branches", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleBranches)))
	mux.Handle("PUT /api/projects/{id}/branches", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleSetBranches)))
	mux.Handle("DELETE /api/projects/{id}/branches/{branch...}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleDeleteBranch)))
	mux.Handle("GET /api/projects/{id}/sandboxes", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleListSandboxes)))
	mux.Handle("POST /api/projects/{id}/sandboxes", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleCreateSandbox)))
	mux.Handle("POST /api/projects/{id}/sandboxes/{workspaceId}/publish", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandlePublishSandbox)))
	mux.Handle("POST /api/projects/{id}/sandboxes/{workspaceId}/start", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleStartSandbox)))
	mux.Handle("GET /api/projects/{id}/compare", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleCompare)))
	mux.Handle("GET /api/projects/{id}/diff", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleDiff)))
	mux.Handle("GET /api/projects/{id}/file", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleFile)))
	mux.Handle("POST /api/projects/{id}/push", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandlePush)))
	mux.Handle("GET /api/projects/{id}/upstream", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleUpstreamStatus)))
	mux.Handle("PUT /api/projects/{id}/upstream", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleSetUpstream)))
	mux.Handle("POST /api/projects/{id}/upstream/sync", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleSyncUpstream)))
	mux.Handle("GET /api/projects/{id}/workspace", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleGetWorkspace)))
	mux.Handle("POST /api/projects/{id}/workspace", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleOpenWorkspace)))
	mux.Handle("POST /api/projects/{id}/workspace/checkout", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleCheckoutWorkspaceBranch)))
	mux.Handle("POST /api/projects/{id}/workspace/publish", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandlePublishWorkspaceBranch)))
	mux.Handle("POST /api/projects/{id}/workspace/sync", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleSyncWorkspace)))
	mux.Handle("GET /api/projects/{id}/workspace/branch-status", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleWorkspaceBranchStatus)))
	mux.Handle("GET /api/projects/{id}/workspace/setup", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleWorkspaceSetup)))
	mux.Handle("POST /api/projects/{id}/workspace/setup", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleRunWorkspaceSetup)))
	mux.Handle("POST /api/projects/{id}/workspace/rebuild", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleRebuildWorkspace)))
	mux.Handle("GET /api/projects/{id}/environment", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleEnvironment)))
	mux.Handle("POST /api/projects/{id}/environment/build", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleBuildEnvironment)))
	mux.Handle("GET /api/projects/{id}/services", h.authMiddleware.RequireAuth(http.HandlerFunc(h.projectServices.HandleList)))
	mux.Handle("POST /api/projects/{id}/services", h.authMiddleware.RequireAuth(http.HandlerFunc(h.projectServices.HandleAdd)))
	mux.Handle("DELETE /api/projects/{id}/services/{type}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.projectServices.HandleRemove)))
	mux.Handle("POST /api/projects/{id}/services/adopt", h.authMiddleware.RequireAuth(http.HandlerFunc(h.projectServices.HandleAdopt)))
	mux.Handle("POST /api/projects/{id}/workspace/services", h.authMiddleware.RequireAuth(http.HandlerFunc(h.projectServices.HandleAddToWorkspace)))
	mux.Handle("GET /api/projects/{id}/git-activity", h.authMiddleware.RequireAuth(http.HandlerFunc(h.gitBridge.HandleActivity)))
	mux.Handle("GET /api/workspaces/{id}/conversion", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleDiscoverConversion)))
	mux.Handle("POST /api/workspaces/{id}/convert", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleConvertWorkspace)))
	mux.Handle("GET /api/projects/{id}/conversion", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleConversionNotice)))
	mux.Handle("POST /api/projects/{id}/conversion/dismiss", h.authMiddleware.RequireAuth(http.HandlerFunc(h.project.HandleDismissConversionNotice)))
	mux.Handle("GET /api/projects/{id}/tasks", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleList)))
	mux.Handle("POST /api/projects/{id}/tasks", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleCreate)))
	mux.Handle("POST /api/projects/{id}/tasks/handoff", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleHandoff)))
	mux.Handle("GET /api/projects/{id}/tasks/{taskId}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleGet)))
	mux.Handle("PUT /api/projects/{id}/tasks/{taskId}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleUpdate)))
	mux.Handle("PUT /api/projects/{id}/tasks/{taskId}/budget", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleSetBudget)))
	mux.Handle("DELETE /api/projects/{id}/tasks/{taskId}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleDelete)))
	mux.Handle("POST /api/projects/{id}/tasks/{taskId}/start", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleStart)))
	mux.Handle("POST /api/projects/{id}/tasks/{taskId}/cancel", h.authMiddleware.RequireAuth(http.HandlerFunc(h.task.HandleCancel)))
	mux.Handle("GET /api/projects/{id}/merge-requests", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleList)))
	mux.Handle("POST /api/projects/{id}/merge-requests", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleCreate)))
	mux.Handle("GET /api/projects/{id}/merge-requests/{mrId}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleGet)))
	mux.Handle("GET /api/projects/{id}/merge-requests/{mrId}/diff", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleDiff)))
	mux.Handle("GET /api/projects/{id}/merge-requests/{mrId}/commits", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleCommits)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/reviews", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleReview)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/comments", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleComment)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/comments/{commentId}/resolve", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleResolveThread)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/agent-reviews", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleAgentReview)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/agent-reviews/{reviewId}/continue", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleContinueReview)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/merge", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleMerge)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/close", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleClose)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/resolve-conflicts", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleResolveConflicts)))
	mux.Handle("POST /api/projects/{id}/merge-requests/{mrId}/send-back", h.authMiddleware.RequireAuth(http.HandlerFunc(h.mr.HandleSendBack)))

	// AI API routes
	mux.Handle("GET /api/ai/models", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListModels)))
	mux.Handle("GET /api/ai/agents", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListAgents)))
	mux.Handle("POST /api/ai/agents", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleCreateAgent)))
	mux.Handle("PUT /api/ai/agents/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleUpdateAgent)))
	mux.Handle("DELETE /api/ai/agents/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleDeleteAgent)))
	mux.Handle("POST /api/ai/chat", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleChat)))
	mux.Handle("POST /api/ai/agent", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleAgentChat)))
	mux.Handle("POST /api/ai/agent/approve", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleApproveCommand)))
	mux.Handle("POST /api/ai/agent/questions/answer", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleAnswerQuestion)))
	mux.Handle("POST /api/ai/agent/runs", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleCreateAgentRun)))
	mux.Handle("GET /api/ai/agent/runs", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListAgentRuns)))
	mux.Handle("GET /api/ai/agent/runs/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleGetAgentRun)))
	mux.Handle("GET /api/ai/agent/runs/{id}/events", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleAgentRunEvents)))
	mux.Handle("POST /api/ai/agent/runs/{id}/cancel", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleCancelAgentRun)))
	mux.Handle("GET /api/ai/agent/attention", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListAttention)))
	mux.Handle("GET /api/ai/usage", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleUsageReport)))

	// AI conversation history routes
	mux.Handle("GET /api/ai/conversations", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListConversations)))
	mux.Handle("POST /api/ai/conversations", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleCreateConversation)))
	mux.Handle("DELETE /api/ai/conversations/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleDeleteConversation)))
	mux.Handle("GET /api/ai/conversations/{id}/messages", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleGetMessages)))
	mux.Handle("POST /api/ai/attachments", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleUploadAttachment)))
	mux.Handle("GET /api/ai/attachments/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleGetAttachment)))

	// Each user's own AI settings: provider keys, agent defaults, budgets
	// and the summary model.
	mux.Handle("GET /api/ai/providers", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListProviders)))
	mux.Handle("PUT /api/ai/providers/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleSetProviderKey)))
	mux.Handle("DELETE /api/ai/providers/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleRemoveProviderKey)))
	mux.Handle("GET /api/ai/agent-defaults", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleListAgentDefaults)))
	mux.Handle("PUT /api/ai/agent-defaults/{id}", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleSetAgentDefaults)))
	mux.Handle("GET /api/ai/budgets", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleGetBudgetDefaults)))
	mux.Handle("PUT /api/ai/budgets", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleSetBudgetDefaults)))
	mux.Handle("GET /api/ai/summary-model", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleGetSummaryModel)))
	mux.Handle("PUT /api/ai/summary-model", h.authMiddleware.RequireAuth(http.HandlerFunc(h.ai.HandleSetSummaryModel)))

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

// newCipher builds the cipher secrets are sealed with from the configured
// key, which is required: devpad will not start without one rather than
// seal secrets with a key that is lost when it stops.
func newCipher(key string) (*encrypt.Cipher, error) {
	if key == "" {
		return nil, fmt.Errorf("an encryption key is required: set --encryption-key or DEVPAD_ENCRYPTION_KEY to a key made once with 'openssl rand -hex 32' and keep it, since SSH and AI provider keys sealed with it cannot be read with any other")
	}
	cipher, err := encrypt.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("invalid encryption key: %w", err)
	}
	return cipher, nil
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
// 10MB plus multipart overhead for workspace file uploads and AI image
// attachments, and 1MB for all other endpoints.
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
			if isMultipartUpload(r) {
				limit = largeBodyLimit + multipartOverheadLimit
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
