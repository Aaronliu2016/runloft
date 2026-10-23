package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRepositoryURL(t *testing.T) {
	tests := []struct {
		url   string
		valid bool
	}{
		{"https://github.com/acme/project.git", true},
		{"ssh://git@github.com/acme/project.git", true},
		{"git@github.com:acme/project.git", true},
		{"http://github.com/acme/project.git", false},
		{"https://git@github.com/acme/project.git", false},
		{"ssh://git:secret@github.com/acme/project.git", false},
		{"https://user:secret@github.com/acme/project.git", false},
		{"https://github.com/acme/project.git?token=secret", false},
		{"user:secret@github.com:acme/project.git", false},
		{"-oProxyCommand=evil", false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			err := ValidateRepositoryURL(tc.url)
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateRepositoryURL() error = %v, valid = %t", err, tc.valid)
			}
		})
	}
}

func TestRepositoryName(t *testing.T) {
	for input, want := range map[string]string{
		"https://github.com/acme/project.git": "project",
		"git@github.com:acme/project.git":     "project",
	} {
		got, err := RepositoryName(input)
		if err != nil || got != want {
			t.Errorf("RepositoryName(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestRepositoryNameRejectsUnsafeDestinationSegments(t *testing.T) {
	for _, input := range []string{
		"https://github.com/acme/..",
		"https://github.com/acme/CON",
		"https://github.com/acme/COM1.txt",
		"https://github.com/acme/foo%3Abar",
		"user:secret@github.com:acme/project.git",
	} {
		if name, err := RepositoryName(input); err == nil {
			t.Errorf("RepositoryName(%q) = %q; want rejection", input, name)
		}
	}
}

func TestSafeFastForwardAndDirtyWorktree(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	source := filepath.Join(root, "source")
	clone := filepath.Join(root, "clone")
	git(t, root, "init", "--bare", bare)
	git(t, root, "init", "--initial-branch=main", source)
	git(t, source, "config", "user.name", "RunLoft Test")
	git(t, source, "config", "user.email", "runloft@example.invalid")
	write(t, filepath.Join(source, "README.md"), "first\n")
	git(t, source, "add", "README.md")
	git(t, source, "commit", "-m", "first")
	git(t, source, "remote", "add", "origin", bare)
	git(t, source, "push", "-u", "origin", "main")
	git(t, root, "--git-dir", bare, "symbolic-ref", "HEAD", "refs/heads/main")
	git(t, root, "clone", bare, clone)
	git(t, clone, "config", "user.name", "RunLoft Test")
	git(t, clone, "config", "user.email", "runloft@example.invalid")

	g := Git{HooksDirectory: t.TempDir()}
	clean, err := g.Status(ctx, clone)
	if err != nil || !clean.Clean() || clean.Branch != "main" {
		t.Fatalf("initial Status() = %+v, %v", clean, err)
	}
	write(t, filepath.Join(source, "README.md"), "second\n")
	git(t, source, "commit", "-am", "second")
	git(t, source, "push", "origin", "main")
	plan, err := g.CheckUpdates(ctx, clone)
	if err != nil || plan.Behind != 1 || plan.Ahead != 0 {
		t.Fatalf("CheckUpdates() = %+v, %v; want one fast-forward commit", plan, err)
	}
	if err := g.FastForward(ctx, clone, plan); err != nil {
		t.Fatal(err)
	}
	updated, err := g.Status(ctx, clone)
	if err != nil || updated.Commit != plan.TargetCommit || !updated.Clean() {
		t.Fatalf("after FastForward Status() = %+v, %v", updated, err)
	}
	if err := g.FastForward(ctx, clone, plan); err == nil || !strings.Contains(err.Error(), "HEAD changed") {
		t.Fatalf("stale FastForward() error = %v; want stale HEAD rejection", err)
	}

	write(t, filepath.Join(clone, "local.txt"), "keep me\n")
	if _, err := g.CheckUpdates(ctx, clone); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty CheckUpdates() error = %v; want dirty worktree rejection", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "local.txt")); err != nil {
		t.Fatalf("local file was changed or deleted: %v", err)
	}
}

func TestCheckUpdatesRejectsDivergedBranch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	source := filepath.Join(root, "source")
	clone := filepath.Join(root, "clone")
	git(t, root, "init", "--bare", bare)
	git(t, root, "init", "--initial-branch=main", source)
	git(t, source, "config", "user.name", "RunLoft Test")
	git(t, source, "config", "user.email", "runloft@example.invalid")
	write(t, filepath.Join(source, "README.md"), "first\n")
	git(t, source, "add", "README.md")
	git(t, source, "commit", "-m", "first")
	git(t, source, "remote", "add", "origin", bare)
	git(t, source, "push", "-u", "origin", "main")
	git(t, root, "--git-dir", bare, "symbolic-ref", "HEAD", "refs/heads/main")
	git(t, root, "clone", bare, clone)
	git(t, clone, "config", "user.name", "RunLoft Test")
	git(t, clone, "config", "user.email", "runloft@example.invalid")

	write(t, filepath.Join(clone, "local.txt"), "local\n")
	git(t, clone, "add", "local.txt")
	git(t, clone, "commit", "-m", "local commit")
	write(t, filepath.Join(source, "README.md"), "remote\n")
	git(t, source, "commit", "-am", "remote commit")
	git(t, source, "push", "origin", "main")

	g := Git{HooksDirectory: t.TempDir()}
	if _, err := g.CheckUpdates(ctx, clone); err == nil || !strings.Contains(err.Error(), "local commit") {
		t.Fatalf("CheckUpdates() error = %v; want divergence rejection", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
