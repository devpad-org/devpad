package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// fsEvent represents a filesystem change sent to WebSocket clients.
type fsEvent struct {
	Type  string `json:"type"` // "create", "write", "remove", "rename"
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

// watcher manages recursive filesystem watching and broadcasts events to
// connected WebSocket clients.
type watcher struct {
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	clients map[*websocket.Conn]struct{}
}

func newWatcher() (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &watcher{
		fsw:     fsw,
		clients: make(map[*websocket.Conn]struct{}),
	}

	// Recursively add all existing directories under workspaceRoot.
	if err := w.addRecursive(workspaceRoot); err != nil {
		fsw.Close()
		return nil, err
	}

	go w.loop()
	return w, nil
}

// addRecursive adds dir and all subdirectories to the watcher.
func (w *watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible directories
		}
		if d.IsDir() {
			if err := w.fsw.Add(path); err != nil {
				log.Printf("watcher: failed to add %s: %v", path, err)
			}
		}
		return nil
	})
}

// loop processes fsnotify events and broadcasts them to clients.
func (w *watcher) loop() {
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

func (w *watcher) handleEvent(ev fsnotify.Event) {
	var eventType string
	switch {
	case ev.Has(fsnotify.Create):
		eventType = "create"
		// If a new directory was created, start watching it recursively.
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = w.addRecursive(ev.Name)
		}
	case ev.Has(fsnotify.Write):
		eventType = "write"
	case ev.Has(fsnotify.Remove):
		eventType = "remove"
	case ev.Has(fsnotify.Rename):
		eventType = "rename"
	default:
		return
	}

	isDir := false
	if info, err := os.Stat(ev.Name); err == nil {
		isDir = info.IsDir()
	}

	event := fsEvent{
		Type:  eventType,
		Path:  ev.Name,
		Name:  filepath.Base(ev.Name),
		IsDir: isDir,
	}

	w.broadcast(event)
}

func (w *watcher) broadcast(event fsEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for conn := range w.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			delete(w.clients, conn)
		}
	}
}

// Close shuts down the fsnotify watcher, releasing inotify file descriptors.
func (w *watcher) Close() error {
	return w.fsw.Close()
}

// addClient registers a new WebSocket client for event notifications.
func (w *watcher) addClient(conn *websocket.Conn) {
	w.mu.Lock()
	w.clients[conn] = struct{}{}
	w.mu.Unlock()
}

// removeClient unregisters a WebSocket client.
func (w *watcher) removeClient(conn *websocket.Conn) {
	w.mu.Lock()
	delete(w.clients, conn)
	w.mu.Unlock()
}

// handleWatch returns an HTTP handler that upgrades to WebSocket and streams
// filesystem events to the client.
func handleWatch(w *watcher) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(rw, r, nil)
		if err != nil {
			log.Printf("watch websocket upgrade: %v", err)
			return
		}

		w.addClient(conn)
		defer func() {
			w.removeClient(conn)
			conn.Close()
		}()

		// Keep reading to detect client disconnect.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}
}
