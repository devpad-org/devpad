package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

func handleStartCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Command == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}

	cwd, err := validatePath(req.CWD)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid cwd: "+err.Error())
		return
	}

	result, err := commandSessions.Start(req.Command, cwd)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errTooManyCommandSessions) {
			status = http.StatusTooManyRequests
		}
		writeErr(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func handleCommandStatus(w http.ResponseWriter, r *http.Request) {
	result, err := commandSessions.Status(r.PathValue("id"))
	if err != nil {
		writeCommandSessionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleReadCommandOutput(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	cursor, err := parseInt64Query(query, "cursor", 0)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	maxBytes, err := parseIntQuery(query, "max_bytes", defaultCommandReadBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	waitMS, err := parseIntQuery(query, "wait_ms", 0)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := commandSessions.ReadOutput(r.Context(), r.PathValue("id"), cursor, maxBytes, waitMS)
	if err != nil {
		writeCommandSessionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func handleStopCommand(w http.ResponseWriter, r *http.Request) {
	result, err := commandSessions.Stop(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCommandSessionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeCommandSessionErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errCommandSessionNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

func parseIntQuery(query url.Values, key string, fallback int) (int, error) {
	raw := query.Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}

func parseInt64Query(query url.Values, key string, fallback int64) (int64, error) {
	raw := query.Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}
