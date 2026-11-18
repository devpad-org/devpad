package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var pidPattern = regexp.MustCompile(`pid=(\d+)\r?\n`)

// openTerminal connects to a new terminal running sh and returns it with
// its shell's pid.
func openTerminal(t *testing.T) (*websocket.Conn, int) {
	t.Helper()
	oldRoot := workspaceRoot
	workspaceRoot = t.TempDir()
	t.Cleanup(func() { workspaceRoot = oldRoot })
	t.Setenv("SHELL", "/bin/sh")

	srv := httptest.NewServer(http.HandlerFunc(handleTerminal))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	send(t, conn, "echo pid=$$\n")
	var out strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (output %q)", err, out.String())
		}
		out.Write(msg)
		if m := pidPattern.FindStringSubmatch(out.String()); m != nil {
			pid, _ := strconv.Atoi(m[1])
			conn.SetReadDeadline(time.Time{})
			return conn, pid
		}
	}
	t.Fatalf("no pid in %q", out.String())
	return nil, 0
}

func send(t *testing.T, conn *websocket.Conn, input string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func waitForExit(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("shell %d still running", pid)
}

// processAlive reports whether pid is a live process, not a zombie.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(stat))
	return len(fields) > 2 && fields[2] != "Z"
}

// Closing a terminal's socket ends its shell, even one sitting idle at its
// prompt: a new shell must not leave the old one running.
func TestClosingATerminalEndsItsShell(t *testing.T) {
	conn, pid := openTerminal(t)
	conn.Close()
	waitForExit(t, pid, time.Second)
}

// A shell that ignores the hangup is killed after the grace period.
func TestClosingATerminalKillsAShellIgnoringTheHangup(t *testing.T) {
	conn, pid := openTerminal(t)
	send(t, conn, "trap '' HUP\n")
	time.Sleep(200 * time.Millisecond)
	conn.Close()
	waitForExit(t, pid, shellHangupGrace+2*time.Second)
}

// A shell that exits closes its terminal's socket.
func TestAShellExitingClosesItsTerminal(t *testing.T) {
	conn, pid := openTerminal(t)
	send(t, conn, "exit\n")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatal("the socket stayed open after the shell exited")
			}
			break
		}
	}
	waitForExit(t, pid, time.Second)
}
