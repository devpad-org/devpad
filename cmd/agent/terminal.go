package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Agent is only accessible on internal Docker network
	},
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func handleTerminal(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	cmd := exec.Command(shell)
	cmd.Env = processEnv("TERM=xterm-256color")
	cmd.Dir = workspaceRoot

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("pty start: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Failed to start terminal\r\n"))
		return
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()

	// The session ends when either side does: the socket closing (a new
	// shell, a closed tab) or the shell exiting.
	var once sync.Once
	ended := make(chan struct{})
	end := func() { once.Do(func() { close(ended) }) }

	// PTY stdout → WebSocket
	go func() {
		defer end()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF && !errors.Is(err, syscall.EIO) && !errors.Is(err, os.ErrClosed) {
					log.Printf("pty read: %v", err)
				}
				return
			}
		}
	}()

	// WebSocket → PTY stdin
	go func() {
		defer end()
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			if msgType == websocket.TextMessage {
				var resize resizeMsg
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					if resize.Cols > 0 && resize.Rows > 0 {
						pty.Setsize(ptmx, &pty.Winsize{
							Rows: resize.Rows,
							Cols: resize.Cols,
						})
					}
					continue
				}
				ptmx.Write(msg)
			} else if msgType == websocket.BinaryMessage {
				ptmx.Write(msg)
			}
		}
	}()

	<-ended
	hangUp(cmd, exited)
	conn.Close()
	ptmx.Close()
}

// shellHangupGrace is how long a shell has to end after its terminal hangs
// up before it is killed.
const shellHangupGrace = 2 * time.Second

// hangUp ends a terminal's shell as closing a real terminal would: SIGHUP to
// its process group (pty.Start makes the shell a session leader), which a
// shell passes on to its jobs, then SIGKILL if it is still running after a
// grace period. Waiting on the PTY alone is not enough: a read blocked on
// it holds it open, so an idle shell would never see the hangup.
func hangUp(cmd *exec.Cmd, exited <-chan struct{}) {
	select {
	case <-exited:
		return
	default:
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
	select {
	case <-exited:
	case <-time.After(shellHangupGrace):
		_ = cmd.Process.Kill()
		<-exited
	}
}
