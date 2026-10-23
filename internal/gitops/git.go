package gitops

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var scpURL = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?(?:[A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\]):[^:\s?#]+$`)

type Git struct {
	HooksDirectory string
}

type Snapshot struct {
	Branch     string
	Commit     string
	Upstream   string
	DirtyPaths []string
}

func (s Snapshot) Clean() bool { return len(s.DirtyPaths) == 0 }

type UpdatePlan struct {
	BaseCommit   string
	TargetCommit string
	TargetRef    string
	Ahead        int
	Behind       int
}

func ValidateRepositoryURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return errors.New("repository URL is required")
	}
	if isSCPURL(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("repository URL must not contain credentials, query parameters, or fragments")
	}
	if u.Scheme != "https" && u.Scheme != "ssh" {
		return errors.New("repository URL must use HTTPS or SSH")
	}
	if u.User != nil {
		username := u.User.Username()
		_, hasPassword := u.User.Password()
		if u.Scheme != "ssh" || username != "git" || hasPassword {
			return errors.New("repository URL must not contain credentials")
		}
	}
	if u.Path == "" || u.Path == "/" {
		return errors.New("repository URL must include a repository path")
	}
	return nil
}

func RepositoryName(raw string) (string, error) {
	if err := ValidateRepositoryURL(raw); err != nil {
		return "", err
	}
	name := ""
	if isSCPURL(raw) {
		name = filepath.Base(strings.SplitN(raw, ":", 2)[1])
	} else {
		u, _ := url.Parse(raw)
		name = filepath.Base(u.Path)
	}
	name = strings.TrimSuffix(name, ".git")
	if !validRepositoryName(name) {
		return "", errors.New("repository URL does not contain a usable project name")
	}
	return name, nil
}

func validRepositoryName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	if strings.ContainsAny(name, `/\\<>:"|?*`) || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "-") {
		return false
	}
	upper := strings.ToUpper(name)
	device := strings.SplitN(upper, ".", 2)[0]
	if device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" {
		return false
	}
	if len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '1' && device[3] <= '9' {
		return false
	}
	return true
}

func isSCPURL(raw string) bool {
	return !strings.Contains(raw, "://") && scpURL.MatchString(raw)
}

func (g Git) Clone(ctx context.Context, rawURL, destination string) error {
	if err := ValidateRepositoryURL(rawURL); err != nil {
		return err
	}
	if strings.TrimSpace(destination) == "" {
		return errors.New("destination path is required")
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("destination already exists; choose a new empty path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}
	if err := g.run(ctx, parent, "clone", "--", rawURL, destination); err != nil {
		return fmt.Errorf("clone repository: %w", err)
	}
	return nil
}

func (g Git) Status(ctx context.Context, root string) (Snapshot, error) {
	var s Snapshot
	var err error
	if s.Branch, err = g.output(ctx, root, "branch", "--show-current"); err != nil {
		return s, err
	}
	if s.Commit, err = g.output(ctx, root, "rev-parse", "HEAD"); err != nil {
		return s, err
	}
	if s.Upstream, err = g.output(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		s.Upstream = ""
	}
	status, err := g.output(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(status, "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" {
			if len(line) > 3 {
				s.DirtyPaths = append(s.DirtyPaths, line[3:])
			} else {
				s.DirtyPaths = append(s.DirtyPaths, line)
			}
		}
	}
	return s, nil
}

func (g Git) OriginURL(ctx context.Context, root string) (string, error) {
	value, err := g.output(ctx, root, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	if err := ValidateRepositoryURL(value); err != nil {
		return "", fmt.Errorf("origin URL: %w", err)
	}
	return value, nil
}

func (g Git) CheckUpdates(ctx context.Context, root string) (UpdatePlan, error) {
	status, err := g.Status(ctx, root)
	if err != nil {
		return UpdatePlan{}, err
	}
	if !status.Clean() {
		return UpdatePlan{}, fmt.Errorf("working tree has local changes: %s", strings.Join(status.DirtyPaths, ", "))
	}
	if status.Branch == "" {
		return UpdatePlan{}, errors.New("detached HEAD cannot be updated automatically")
	}
	if err := g.run(ctx, root, "fetch", "--prune", "origin"); err != nil {
		return UpdatePlan{}, fmt.Errorf("fetch origin: %w", err)
	}
	upstream := status.Upstream
	if upstream == "" {
		upstream = "origin/" + status.Branch
	}
	target, err := g.output(ctx, root, "rev-parse", "--verify", upstream)
	if err != nil {
		return UpdatePlan{}, fmt.Errorf("find upstream %q: %w", upstream, err)
	}
	counts, err := g.output(ctx, root, "rev-list", "--left-right", "--count", "HEAD..."+upstream)
	if err != nil {
		return UpdatePlan{}, err
	}
	var plan UpdatePlan
	if _, err := fmt.Sscanf(counts, "%d\t%d", &plan.Ahead, &plan.Behind); err != nil {
		if _, err = fmt.Sscanf(counts, "%d %d", &plan.Ahead, &plan.Behind); err != nil {
			return UpdatePlan{}, fmt.Errorf("parse upstream distance %q: %w", counts, err)
		}
	}
	plan.BaseCommit = status.Commit
	plan.TargetCommit = target
	plan.TargetRef = upstream
	if plan.Ahead > 0 {
		return plan, fmt.Errorf("branch has %d local commit(s) not present in %s; automatic update is blocked", plan.Ahead, upstream)
	}
	return plan, nil
}

func (g Git) FastForward(ctx context.Context, root string, plan UpdatePlan) error {
	status, err := g.Status(ctx, root)
	if err != nil {
		return err
	}
	if !status.Clean() {
		return fmt.Errorf("working tree has local changes: %s", strings.Join(status.DirtyPaths, ", "))
	}
	if status.Commit != plan.BaseCommit {
		return errors.New("HEAD changed after update review; check for updates again")
	}
	target, err := g.output(ctx, root, "rev-parse", "--verify", plan.TargetRef)
	if err != nil || target != plan.TargetCommit {
		return errors.New("upstream changed after update review; check for updates again")
	}
	if plan.Ahead != 0 {
		return errors.New("local commits prevent a fast-forward update")
	}
	if plan.Behind == 0 {
		return nil
	}
	if err := g.run(ctx, root, "merge", "--ff-only", plan.TargetRef); err != nil {
		return fmt.Errorf("fast-forward update: %w", err)
	}
	return nil
}

func (g Git) output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := g.command(ctx, dir, args...)
	out, err := cmd.CombinedOutput()
	value := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("git %s: %w%s", strings.Join(args, " "), err, outputSuffix(value))
	}
	return value, nil
}

func (g Git) run(ctx context.Context, dir string, args ...string) error {
	_, err := g.output(ctx, dir, args...)
	return err
}

func (g Git) command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	if g.HooksDirectory != "" {
		args = append([]string{"-c", "core.hooksPath=" + g.HooksDirectory}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd
}

func outputSuffix(value string) string {
	if value == "" {
		return ""
	}
	return ": " + value
}
