package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	procRoot            = "/proc"
	clockTicksPerSecond = 100
)

type processInfo struct {
	PID            int    `json:"pid"`
	PPID           int    `json:"ppid"`
	User           string `json:"user"`
	State          string `json:"state"`
	Command        string `json:"command"`
	MemoryBytes    uint64 `json:"memoryBytes"`
	CPUTimeSeconds uint64 `json:"cpuTimeSeconds"`
	Killable       bool   `json:"killable"`
}

type processListResponse struct {
	Processes []processInfo `json:"processes"`
}

func handleListProcesses(w http.ResponseWriter, r *http.Request) {
	processes, err := listProcesses(procRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list processes")
		return
	}
	writeJSON(w, http.StatusOK, processListResponse{Processes: processes})
}

func handleKillProcess(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid process id")
		return
	}
	if !isKillableProcess(pid) {
		writeErr(w, http.StatusBadRequest, "process cannot be killed from Devpad")
		return
	}

	err = syscall.Kill(pid, syscall.SIGTERM)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "signaled"})
		return
	}
	if errors.Is(err, syscall.ESRCH) {
		writeErr(w, http.StatusNotFound, "process not found")
		return
	}
	if errors.Is(err, syscall.EPERM) {
		writeErr(w, http.StatusForbidden, "permission denied")
		return
	}
	writeErr(w, http.StatusInternalServerError, "failed to kill process")
}

func listProcesses(root string) ([]processInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading proc root: %w", err)
	}

	users := loadPasswdUsers("/etc/passwd")
	processes := make([]processInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		process, err := parseProcess(root, pid, users)
		if err != nil {
			continue
		}
		processes = append(processes, process)
	}

	sort.Slice(processes, func(i, j int) bool {
		if processes[i].MemoryBytes == processes[j].MemoryBytes {
			return processes[i].PID < processes[j].PID
		}
		return processes[i].MemoryBytes > processes[j].MemoryBytes
	})
	return processes, nil
}

func parseProcess(root string, pid int, users map[string]string) (processInfo, error) {
	dir := filepath.Join(root, strconv.Itoa(pid))

	stat, err := parseProcStat(filepath.Join(dir, "stat"))
	if err != nil {
		return processInfo{}, err
	}

	uid, rssKB := parseProcStatus(filepath.Join(dir, "status"))
	command := readProcCommand(filepath.Join(dir, "cmdline"), stat.command)

	user := uid
	if name, ok := users[uid]; ok {
		user = name
	}

	return processInfo{
		PID:            pid,
		PPID:           stat.ppid,
		User:           user,
		State:          stat.state,
		Command:        command,
		MemoryBytes:    rssKB * 1024,
		CPUTimeSeconds: (stat.utime + stat.stime) / clockTicksPerSecond,
		Killable:       isKillableProcess(pid),
	}, nil
}

type procStat struct {
	command string
	state   string
	ppid    int
	utime   uint64
	stime   uint64
}

func parseProcStat(path string) (procStat, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return procStat{}, fmt.Errorf("reading stat: %w", err)
	}

	text := strings.TrimSpace(string(data))
	open := strings.IndexByte(text, '(')
	close := strings.LastIndex(text, ") ")
	if open < 0 || close < open {
		return procStat{}, fmt.Errorf("invalid stat format")
	}

	fields := strings.Fields(text[close+2:])
	if len(fields) < 13 {
		return procStat{}, fmt.Errorf("invalid stat field count")
	}

	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return procStat{}, fmt.Errorf("parsing ppid: %w", err)
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("parsing utime: %w", err)
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("parsing stime: %w", err)
	}

	return procStat{
		command: text[open+1 : close],
		state:   fields[0],
		ppid:    ppid,
		utime:   utime,
		stime:   stime,
	}, nil
}

func parseProcStatus(path string) (uid string, rssKB uint64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "Uid":
			uid = fields[1]
		case "VmRSS":
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				rssKB = value
			}
		}
	}
	return uid, rssKB
}

func readProcCommand(path, fallback string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "[" + fallback + "]"
	}
	command := strings.Trim(strings.ReplaceAll(string(data), "\x00", " "), " ")
	if command == "" {
		return "[" + fallback + "]"
	}
	return command
}

func loadPasswdUsers(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	users := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		users[fields[2]] = fields[0]
	}
	return users
}

func isKillableProcess(pid int) bool {
	return pid > 1 && pid != os.Getpid()
}
