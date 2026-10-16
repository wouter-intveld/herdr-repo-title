package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_ALLOW_PROTOCOL=file",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The layouts are built by git itself so the test tracks what git writes.
func TestTitle(t *testing.T) {
	root := t.TempDir()

	repo := mkdir(t, root, "my-repo")
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	mkdir(t, repo, "src", "deep")

	// Linked worktree living outside the main checkout, under another name.
	git(t, repo, "worktree", "add", "-q", filepath.Join(root, "wt", "feature-x"))

	// Bare clone with a worktree beside it (`git clone --bare`).
	git(t, root, "clone", "-q", "--bare", repo, "bare-repo.git")
	git(t, filepath.Join(root, "bare-repo.git"), "worktree", "add", "-q", filepath.Join(root, "bare-wt"))

	// `.bare` + `.git` file convention for worktree-first checkouts.
	dotbare := mkdir(t, root, "dotbare-repo")
	git(t, dotbare, "clone", "-q", "--bare", repo, ".bare")
	if err := os.WriteFile(filepath.Join(dotbare, ".git"), []byte("gitdir: ./.bare\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dotbare, "worktree", "add", "-q", "main-wt")

	// Submodule: named after the submodule, not the superproject.
	super := mkdir(t, root, "super")
	git(t, super, "init", "-q", "-b", "main")
	git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", repo, "vendor/lib")

	plain := mkdir(t, root, "not-a-repo", "Downloads")

	tests := []struct {
		dir, want string
	}{
		{repo, "my-repo"},
		{filepath.Join(repo, "src", "deep"), "my-repo"},
		{filepath.Join(root, "wt", "feature-x"), "my-repo"},
		{filepath.Join(root, "bare-wt"), "bare-repo"},
		{filepath.Join(dotbare, "main-wt"), "dotbare-repo"},
		{filepath.Join(super, "vendor", "lib"), "lib"},
		{super, "super"},
		{plain, "Downloads"},
	}
	for _, tc := range tests {
		if got := title(tc.dir); got != tc.want {
			t.Errorf("title(%s) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestManaged(t *testing.T) {
	tests := []struct {
		label, written string
		want           bool
	}{
		{"", "", true},             // unlabelled
		{"3", "", true},            // Herdr's positional default
		{"3", "repo", true},        // reset to default after we named it
		{"repo", "repo", true},     // ours
		{"repo", "", false},        // named by hand before we ever wrote it
		{"my name", "repo", false}, // renamed by hand after we wrote it
		{"v2", "", false},          // digits alone only
	}
	for _, tc := range tests {
		if got := managed(tc.label, tc.written); got != tc.want {
			t.Errorf("managed(%q, %q) = %v, want %v", tc.label, tc.written, got, tc.want)
		}
	}
}
