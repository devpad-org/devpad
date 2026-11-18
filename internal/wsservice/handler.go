package wsservice

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/workspace"
)

// WorkspaceAccess reads a user's workspace and tells its agent about the
// services it has.
type WorkspaceAccess interface {
	workspace.AccessService
	workspace.ServiceEnvSyncer
}

// Handler holds HTTP handlers for workspace service endpoints.
type Handler struct {
	service   Service
	workspace WorkspaceAccess
}

// NewHandler creates a new workspace service Handler.
func NewHandler(service Service, ws WorkspaceAccess) *Handler {
	return &Handler{service: service, workspace: ws}
}

// syncEnv gives a running workspace's new terminals and commands the
// variables of the services it has now, which its container's environment
// lacks for one added since the container was made. A failure leaves the
// service working, reachable by the connection details the IDE shows.
func (h *Handler) syncEnv(r *http.Request, userID, workspaceID int64) {
	if err := h.workspace.SyncServiceEnv(r.Context(), userID, workspaceID); err != nil {
		log.Printf("workspace %d: syncing service variables: %v", workspaceID, err)
	}
}

// HandleList returns all services for a workspace.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	wsID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	// Verify workspace ownership.
	if _, err := h.workspace.Get(r.Context(), user.ID, wsID); err != nil {
		handleWorkspaceError(w, err)
		return
	}

	services, err := h.service.List(r.Context(), wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}

	items := make([]map[string]any, 0, len(services))
	for _, svc := range services {
		items = append(items, serviceResponse(svc))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"services": items,
		"catalog":  h.service.Catalog(r.Context()),
	})
}

// HandleCreate creates a new service for a workspace.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	wsID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	// Verify workspace ownership.
	if _, err := h.workspace.Get(r.Context(), user.ID, wsID); err != nil {
		handleWorkspaceError(w, err)
		return
	}

	var req struct {
		ServiceType string `json:"serviceType"`
		// AcceptLicense is the user accepting the service's licence
		// terms, which a service such as SQL Server requires.
		AcceptLicense bool `json:"acceptLicense"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ServiceType == "" {
		writeError(w, http.StatusBadRequest, "serviceType is required")
		return
	}

	svc, err := h.service.Create(r.Context(), wsID, ServiceType(req.ServiceType), CreateOptions{
		UserID:        user.ID,
		AcceptLicense: req.AcceptLicense,
	})
	if err != nil {
		handleServiceError(w, err)
		return
	}

	// If the workspace is running, start the service immediately.
	ws, _ := h.workspace.Get(r.Context(), user.ID, wsID)
	if ws != nil && ws.Status == workspace.StatusRunning && ws.NetworkName != "" {
		started, err := h.service.Start(r.Context(), svc.ID, ws.NetworkName)
		if err != nil {
			log.Printf("service %d: auto-start failed: %v", svc.ID, err)
		} else {
			svc = started
			h.syncEnv(r, user.ID, wsID)
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{"service": serviceResponse(svc)})
}

// HandleDelete removes a service from a workspace.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	wsID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	// Verify workspace ownership.
	if _, err := h.workspace.Get(r.Context(), user.ID, wsID); err != nil {
		handleWorkspaceError(w, err)
		return
	}

	svcID, err := parseID(r, "serviceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}

	// Verify service belongs to this workspace.
	svc, err := h.service.Get(r.Context(), svcID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	if svc.WorkspaceID != wsID {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	if err := h.service.Delete(r.Context(), svcID); err != nil {
		handleServiceError(w, err)
		return
	}
	h.syncEnv(r, user.ID, wsID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// HandleStart starts a single service container.
func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	wsID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	ws, err := h.workspace.Get(r.Context(), user.ID, wsID)
	if err != nil {
		handleWorkspaceError(w, err)
		return
	}

	svcID, err := parseID(r, "serviceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}

	svc, err := h.service.Get(r.Context(), svcID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	if svc.WorkspaceID != wsID {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	if ws.NetworkName == "" {
		writeError(w, http.StatusConflict, "workspace has no network; start the workspace first")
		return
	}

	started, err := h.service.Start(r.Context(), svcID, ws.NetworkName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start service")
		return
	}
	// Starting it may have made its first container.
	h.syncEnv(r, user.ID, wsID)

	writeJSON(w, http.StatusOK, map[string]any{"service": serviceResponse(started)})
}

// HandleStop stops a single service container.
func (h *Handler) HandleStop(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	wsID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	if _, err := h.workspace.Get(r.Context(), user.ID, wsID); err != nil {
		handleWorkspaceError(w, err)
		return
	}

	svcID, err := parseID(r, "serviceId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}

	svc, err := h.service.Get(r.Context(), svcID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	if svc.WorkspaceID != wsID {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	stopped, err := h.service.Stop(r.Context(), svcID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stop service")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"service": serviceResponse(stopped)})
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func serviceResponse(svc *WorkspaceService) map[string]any {
	var cfg ServiceConfig
	_ = json.Unmarshal([]byte(svc.Config), &cfg)

	// How the IDE shows a client reaching the service. A row whose type
	// devpad no longer defines is shown with its host alone.
	host := sidecarContainerName(svc.WorkspaceID, svc.ServiceType)
	connection := Connection{}
	credentials := []Credential{}
	if def, ok := lookupDefinition(svc.ServiceType); ok {
		connection = def.connection(host, cfg)
		credentials = def.credentialFields(cfg)
	}

	return map[string]any{
		"id":          svc.ID,
		"workspaceId": svc.WorkspaceID,
		"serviceType": svc.ServiceType,
		"status":      svc.Status,
		"host":        host,
		"connection":  connection,
		"credentials": credentials,
		"config": map[string]any{
			"image":       cfg.Image,
			"port":        cfg.Port,
			"defaultUser": cfg.DefaultUser,
			"defaultPass": cfg.DefaultPass,
			"defaultDb":   cfg.DefaultDB,
		},
		"createdAt": svc.CreatedAt,
		"updatedAt": svc.UpdatedAt,
	}
}

func handleServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	if errors.Is(err, ErrDuplicateType) {
		writeError(w, http.StatusConflict, "workspace already has a service of this type")
		return
	}
	if errors.Is(err, ErrInvalidType) {
		writeError(w, http.StatusBadRequest, "unsupported service type; supported: "+strings.Join(SupportedTypes(), ", "))
		return
	}
	if errors.Is(err, ErrLicenseNotAccepted) {
		writeError(w, http.StatusBadRequest, "this service's licence terms must be accepted to deploy it")
		return
	}
	if errors.Is(err, ErrUnsupportedArch) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func handleWorkspaceError(w http.ResponseWriter, err error) {
	if errors.Is(err, workspace.ErrNotFound) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if errors.Is(err, workspace.ErrForbidden) {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
