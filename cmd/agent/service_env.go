package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// A container's environment is fixed when it is made, so a service added
// to a running workspace would otherwise reach its processes only after a
// rebuild. devpad sends the agent the variables of every service the
// workspace has, and the agent gives them to each terminal and command it
// starts, over the container's own. They are kept in a file, so they
// outlast a restart of the container; a rebuilt container starts without
// the file, with every service in its environment.

// maxServiceEnvBody bounds a request setting the services' variables.
const maxServiceEnvBody = 64 << 10

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// serviceEnvStore holds the variables devpad last sent for the workspace's
// services.
type serviceEnvStore struct {
	path string

	mu  sync.RWMutex
	env []string
}

var serviceEnv = newServiceEnvStore(serviceEnvPath())

func serviceEnvPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".devpad", "service-env.json")
}

// newServiceEnvStore reads the variables kept at path, if any.
func newServiceEnvStore(path string) *serviceEnvStore {
	s := &serviceEnvStore{path: path}
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("reading service environment: %v", err)
		}
		return s
	}
	var env []string
	if err := json.Unmarshal(data, &env); err != nil {
		log.Printf("reading service environment: %v", err)
		return s
	}
	if err := validateServiceEnv(env); err != nil {
		log.Printf("reading service environment: %v", err)
		return s
	}
	s.env = env
	return s
}

// set replaces the variables, keeping them for the container's next start.
func (s *serviceEnvStore) set(env []string) error {
	if err := validateServiceEnv(env); err != nil {
		return err
	}
	env = append([]string(nil), env...)
	if s.path != "" {
		data, err := json.Marshal(env)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(s.path), err)
		}
		tmp := s.path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, s.path); err != nil {
			return fmt.Errorf("replacing %s: %w", s.path, err)
		}
	}
	s.mu.Lock()
	s.env = env
	s.mu.Unlock()
	return nil
}

// environ is base with the services' variables over it, then extra over
// both.
func (s *serviceEnvStore) environ(base []string, extra ...string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return mergeEnv(mergeEnv(base, s.env), extra)
}

// mergeEnv returns base with each of over's variables set, replacing one
// of the same name.
func mergeEnv(base, over []string) []string {
	if len(over) == 0 {
		return base
	}
	replaced := make(map[string]bool, len(over))
	for _, kv := range over {
		name, _, _ := strings.Cut(kv, "=")
		replaced[name] = true
	}
	out := make([]string, 0, len(base)+len(over))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if !replaced[name] {
			out = append(out, kv)
		}
	}
	return append(out, over...)
}

func validateServiceEnv(env []string) error {
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !envNamePattern.MatchString(name) {
			return fmt.Errorf("invalid variable %q", name)
		}
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("variable %s contains a NUL byte", name)
		}
	}
	return nil
}

// processEnv is the environment for a process the agent starts for the
// workspace: its own, with the services' variables, then extra.
func processEnv(extra ...string) []string {
	return serviceEnv.environ(os.Environ(), extra...)
}

// handleSetServiceEnv replaces the services' variables.
func handleSetServiceEnv(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Env []string `json:"env"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxServiceEnvBody)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateServiceEnv(req.Env); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := serviceEnv.set(req.Env); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
