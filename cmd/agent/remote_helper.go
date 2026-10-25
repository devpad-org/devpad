package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// remoteHelperName is the name git runs for "devpad::" remote URLs. The
// agent binary is linked under it and acts as the helper when run that way.
const remoteHelperName = "git-remote-devpad"

// runRemoteHelper speaks git's remote-helper protocol (see
// gitremote-helpers(7)) on in and out, turning list, fetch and push into
// requests to devpad through the agent's git bridge. Only the project repo
// at /workspace can use it, since that is the repo devpad bundles from and
// into.
func runRemoteHelper(in io.Reader, out io.Writer, errOut io.Writer) int {
	h := &remoteHelper{out: bufio.NewWriter(out), errOut: errOut, client: bridgeClient(gitBridgeSocketPath())}
	if err := h.checkRepo(); err != nil {
		fmt.Fprintf(errOut, "fatal: %v\n", err)
		return 1
	}
	if err := h.run(bufio.NewReader(in)); err != nil {
		fmt.Fprintf(errOut, "fatal: %v\n", err)
		return 1
	}
	return 0
}

type remoteHelper struct {
	out    *bufio.Writer
	errOut io.Writer
	client *http.Client
}

func bridgeClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
}

func helperWorkspaceRoot() string {
	if root := os.Getenv("DEVPAD_WORKSPACE_ROOT"); root != "" {
		return root
	}
	return workspaceRoot
}

// checkRepo refuses any repo but the project repo at the workspace root,
// including worktrees of other repos.
func (h *remoteHelper) checkRepo() error {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("finding the repository: %w", err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("resolving the repository: %w", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(helperWorkspaceRoot(), ".git"))
	if err != nil || got != want {
		return fmt.Errorf("the devpad remote only works in the project repository at %s", helperWorkspaceRoot())
	}
	return nil
}

func (h *remoteHelper) run(in *bufio.Reader) error {
	for {
		line, err := readLine(in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch {
		case line == "":
			return nil
		case line == "capabilities":
			h.out.WriteString("fetch\npush\n\n")
		case line == "list" || line == "list for-push":
			if err := h.list(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "fetch "):
			if err := h.fetch(line, in); err != nil {
				return err
			}
		case strings.HasPrefix(line, "push "):
			if err := h.push(line, in); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported remote-helper command %q", line)
		}
		if err := h.out.Flush(); err != nil {
			return err
		}
	}
}

func readLine(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// batch reads the rest of a batch of commands that started with first,
// up to the blank line that ends it.
func batch(first string, in *bufio.Reader) ([]string, error) {
	lines := []string{first}
	for {
		line, err := readLine(in)
		if err != nil {
			return nil, err
		}
		if line == "" {
			return lines, nil
		}
		lines = append(lines, line)
	}
}

func (h *remoteHelper) request(req gitBridgeRequest) (gitBridgeResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return gitBridgeResult{}, err
	}
	resp, err := h.client.Post("http://devpad/request", "application/json", bytes.NewReader(body))
	if err != nil {
		return gitBridgeResult{}, fmt.Errorf("reaching the devpad agent: %w", err)
	}
	defer resp.Body.Close()
	var res gitBridgeResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return gitBridgeResult{}, fmt.Errorf("reading the devpad agent's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK && res.Error == "" {
		res.Error = resp.Status
	}
	return res, nil
}

func (h *remoteHelper) list() error {
	res, err := h.request(gitBridgeRequest{Op: "list"})
	if err != nil {
		return err
	}
	if res.Error != "" {
		return fmt.Errorf("devpad: %s", res.Error)
	}
	hasDefault := false
	for _, ref := range res.Refs {
		fmt.Fprintf(h.out, "%s refs/heads/%s\n", ref.SHA, ref.Name)
		hasDefault = hasDefault || ref.Name == res.DefaultBranch
	}
	if hasDefault {
		fmt.Fprintf(h.out, "@refs/heads/%s HEAD\n", res.DefaultBranch)
	}
	h.out.WriteString("\n")
	return nil
}

// fetch asks devpad to bring the listed branches into the repo as
// devpad/<branch>, then checks the commits git asked for arrived.
func (h *remoteHelper) fetch(first string, in *bufio.Reader) error {
	lines, err := batch(first, in)
	if err != nil {
		return err
	}
	var branches []string
	want := map[string]string{}
	seen := map[string]bool{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "fetch" {
			return fmt.Errorf("unexpected fetch command %q", line)
		}
		branch, ok := strings.CutPrefix(fields[2], "refs/heads/")
		if !ok {
			continue
		}
		want[branch] = fields[1]
		if !seen[branch] {
			seen[branch] = true
			branches = append(branches, branch)
		}
	}
	if len(branches) > 0 {
		res, err := h.request(gitBridgeRequest{Op: "fetch", Branches: branches})
		if err != nil {
			return err
		}
		if res.Error != "" {
			return fmt.Errorf("devpad: %s", res.Error)
		}
	}
	for branch, sha := range want {
		if exec.Command("git", "cat-file", "-e", sha+"^{commit}").Run() != nil {
			return fmt.Errorf("%s moved while fetching; fetch again", branch)
		}
	}
	h.out.WriteString("\n")
	return nil
}

// push asks devpad to publish each branch. devpad pulls the branch itself
// and applies the Publish button's rules; the checks here only give clearer
// errors.
func (h *remoteHelper) push(first string, in *bufio.Reader) error {
	lines, err := batch(first, in)
	if err != nil {
		return err
	}
	for _, line := range lines {
		spec, ok := strings.CutPrefix(line, "push ")
		if !ok {
			return fmt.Errorf("unexpected push command %q", line)
		}
		dst, why := h.pushOne(spec)
		if why == "" {
			fmt.Fprintf(h.out, "ok %s\n", dst)
		} else {
			fmt.Fprintf(h.out, "error %s %s\n", dst, oneLine(why))
		}
	}
	h.out.WriteString("\n")
	return nil
}

func (h *remoteHelper) pushOne(spec string) (dst, why string) {
	force := strings.HasPrefix(spec, "+")
	src, dst, _ := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
	branch, ok := strings.CutPrefix(dst, "refs/heads/")
	switch {
	case !ok:
		return dst, "devpad only accepts branches"
	case src == "":
		return dst, "devpad does not delete branches"
	case force:
		return dst, "devpad refuses force pushes; it only fast-forwards"
	}
	srcSHA, err := exec.Command("git", "rev-parse", "--verify", "--quiet", src+"^{commit}").Output()
	if err != nil {
		return dst, "cannot resolve " + src
	}
	localSHA, err := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}").Output()
	if err != nil || strings.TrimSpace(string(localSHA)) != strings.TrimSpace(string(srcSHA)) {
		return dst, "devpad publishes a local branch under its own name; push a local branch called " + branch
	}
	res, err := h.request(gitBridgeRequest{Op: "push", Branches: []string{branch}})
	if err != nil {
		return dst, err.Error()
	}
	return dst, res.Error
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
