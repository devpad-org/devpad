package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Version is set at build time via -ldflags.
var Version = "dev"

const defaultPort = "9100"

var workspaceRoot = "/workspace"

func main() {
	// Linked as git-remote-devpad, the binary is git's helper for the devpad
	// remote, run by git inside the workspace.
	if filepath.Base(os.Args[0]) == remoteHelperName {
		os.Exit(runRemoteHelper(os.Stdin, os.Stdout, os.Stderr))
	}

	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = defaultPort
	}

	authToken := os.Getenv("AGENT_AUTH_TOKEN")
	if authToken == "" {
		log.Fatalf("AGENT_AUTH_TOKEN environment variable is required")
	}

	// Start filesystem watcher
	fsWatcher, err := newWatcher()
	if err != nil {
		log.Fatalf("starting filesystem watcher: %v", err)
	}

	// Authenticated routes — require a valid agent token.
	authedMux := http.NewServeMux()

	// File operations
	authedMux.HandleFunc("GET /api/files", handleListFiles)
	authedMux.HandleFunc("GET /api/file", handleReadFile)
	authedMux.HandleFunc("PUT /api/file", handleWriteFile)
	authedMux.HandleFunc("DELETE /api/file", handleDeleteFile)
	authedMux.HandleFunc("POST /api/file/mkdir", handleMkdir)
	authedMux.HandleFunc("POST /api/file/rename", handleRename)

	// Search and command execution
	authedMux.HandleFunc("POST /api/search", handleSearchFiles)
	authedMux.HandleFunc("POST /api/command", handleRunCommand)
	authedMux.HandleFunc("POST /api/commands", handleStartCommand)
	authedMux.HandleFunc("GET /api/commands/{id}", handleCommandStatus)
	authedMux.HandleFunc("GET /api/commands/{id}/output", handleReadCommandOutput)
	authedMux.HandleFunc("POST /api/commands/{id}/stop", handleStopCommand)
	authedMux.HandleFunc("GET /api/processes", handleListProcesses)
	authedMux.HandleFunc("POST /api/processes/{pid}/kill", handleKillProcess)

	// Git operations
	authedMux.HandleFunc("GET /api/git/status", handleGitStatus)
	authedMux.HandleFunc("GET /api/git/commits", handleGitLog)
	authedMux.HandleFunc("GET /api/git/commit-files", handleGitCommitFiles)
	authedMux.HandleFunc("GET /api/git/commit-diff", handleGitCommitDiff)
	authedMux.HandleFunc("GET /api/git/branches", handleGitBranches)
	authedMux.HandleFunc("GET /api/git/diff", handleGitDiff)
	authedMux.HandleFunc("GET /api/git/file-diff", handleGitFileDiff)
	authedMux.HandleFunc("GET /api/git/remotes", handleGitRemotes)
	authedMux.HandleFunc("POST /api/git/action", handleGitAction)

	// Project repo transfers (bundles between the canonical repo and sandboxes)
	authedMux.HandleFunc("POST /api/repo/init", handleRepoInit)
	authedMux.HandleFunc("GET /api/repo/refs", handleRepoRefs)
	authedMux.HandleFunc("GET /api/repo/bundle", handleRepoBundle)
	authedMux.HandleFunc("GET /api/repo/compare", handleRepoCompare)
	authedMux.HandleFunc("POST /api/repo/seed", handleRepoSeed)
	authedMux.HandleFunc("POST /api/repo/import", handleRepoImport)
	authedMux.HandleFunc("GET /api/repo/diff", handleRepoDiff)
	authedMux.HandleFunc("GET /api/repo/file", handleRepoFile)
	authedMux.HandleFunc("POST /api/repo/merge", handleRepoMerge)
	authedMux.HandleFunc("POST /api/repo/push", handleRepoPush)
	authedMux.HandleFunc("POST /api/repo/fetch-base", handleRepoFetchBase)
	authedMux.HandleFunc("POST /api/repo/fetch-bases", handleRepoFetchBases)
	authedMux.HandleFunc("POST /api/repo/checkout", handleRepoCheckout)
	authedMux.HandleFunc("GET /api/repo/branch-status", handleRepoBranchStatus)
	authedMux.HandleFunc("GET /api/repo/discover", handleRepoDiscover)
	authedMux.HandleFunc("GET /api/repo/bundle-all", handleRepoBundleAll)
	authedMux.HandleFunc("POST /api/repo/init-contents", handleRepoInitContents)
	authedMux.HandleFunc("POST /api/repo/init-contents/undo", handleRepoUndoInitContents)
	authedMux.HandleFunc("POST /api/repo/import-initial", handleRepoImportInitial)
	authedMux.HandleFunc("POST /api/ssh/remove-managed", handleSSHRemoveManaged)
	authedMux.HandleFunc("POST /api/credentials/strip", handleCredentialsStrip)

	// Git bridge: devpad polls for git-remote-devpad's requests.
	authedMux.HandleFunc("GET /api/git-bridge/requests", bridge.handlePoll)
	authedMux.HandleFunc("POST /api/git-bridge/requests/{id}/result", bridge.handleResult)
	authedMux.HandleFunc("POST /api/git-bridge/remote", handleGitBridgeRemote)

	// Terminal WebSocket
	authedMux.HandleFunc("GET /ws/terminal", handleTerminal)

	// Filesystem watch WebSocket
	authedMux.HandleFunc("GET /ws/watch", handleWatch(fsWatcher))

	// Port proxy — allows the Devpad server to reach any port through the agent.
	authedMux.HandleFunc("/proxy/", handlePortProxy)

	// Top-level mux: unauthenticated health/version endpoints + auth-protected routes.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/version", handleVersion)
	mux.Handle("/", requireAuth(authToken, authedMux))

	addr := ":" + port
	log.Printf("devpad-agent listening on %s (workspace: %s)", addr, workspaceRoot)

	srv := &http.Server{Addr: addr, Handler: mux}

	bridgeSrv, err := serveGitBridgeSocket(bridge, gitBridgeSocketPath())
	if err != nil {
		log.Printf("git bridge unavailable: %v", err)
	}

	// Graceful shutdown: close the filesystem watcher and HTTP server on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("shutting down agent...")
		fsWatcher.Close()
		if bridgeSrv != nil {
			bridgeSrv.Shutdown(context.Background())
		}
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"version": Version})
}

// agentAuthHeader is the custom header used to authenticate requests to the agent.
// A dedicated header is used instead of the standard Authorization header so that
// previewed applications can use Authorization (e.g. Bearer JWT) without conflict.
const agentAuthHeader = "X-Devpad-Agent-Token"

// requireAuth is HTTP middleware that validates the agent token on every request.
func requireAuth(token string, next http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := []byte(r.Header.Get(agentAuthHeader))
		if len(provided) == 0 {
			http.Error(w, `{"error":"missing or invalid agent token"}`, http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
			http.Error(w, `{"error":"invalid agent token"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
