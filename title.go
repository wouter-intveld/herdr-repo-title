package main

import (
	"os"
	"path/filepath"
	"strings"
)

// title names a directory after the git repository containing it, or after
// the directory itself outside one. A linked worktree is named after its main
// repository, found through git's common dir, the way `git rev-parse
// --git-common-dir` does, but without spawning git on every poll.
func title(dir string) string {
	if common, ok := gitCommonDir(dir); ok {
		return repoName(common)
	}
	return filepath.Base(dir)
}

// gitCommonDir walks up from dir to the first `.git` entry and resolves the
// repository's common git directory.
func gitCommonDir(dir string) (string, bool) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dotGit := filepath.Join(d, ".git")
		fi, err := os.Stat(dotGit)
		if err == nil {
			if fi.IsDir() {
				return dotGit, true
			}
			// A worktree or submodule: `.git` is a file naming the real git dir.
			gitDir, ok := readGitDirFile(dotGit)
			if !ok {
				return "", false
			}
			// Linked worktrees point at <common>/worktrees/<name>, which holds a
			// `commondir` file; a submodule's git dir is its own common dir.
			if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				return resolve(gitDir, strings.TrimSpace(string(b))), true
			}
			return gitDir, true
		}
		if parent := filepath.Dir(d); parent == d {
			return "", false
		}
	}
}

func readGitDirFile(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", false
	}
	return resolve(filepath.Dir(path), strings.TrimSpace(gitDir)), true
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// repoName turns a common git dir into a repository name:
//
//	~/git/foo/.git    -> foo   (normal checkout)
//	~/git/foo/.bare   -> foo   (bare repo with worktrees beside it)
//	~/git/foo.git     -> foo   (bare repo)
//	super/.git/modules/sub -> sub (submodule)
func repoName(common string) string {
	base := filepath.Base(common)
	if strings.HasPrefix(base, ".") {
		base = filepath.Base(filepath.Dir(common))
	}
	return strings.TrimSuffix(base, ".git")
}

// paneDir is where the pane is working: the foreground process's directory
// when Herdr can read it (a shell's cd, or an agent started elsewhere), else
// the pane's own cwd.
func paneDir(p pane) string {
	if p.ForegroundCwd != nil && *p.ForegroundCwd != "" {
		return *p.ForegroundCwd
	}
	if p.Cwd != nil {
		return *p.Cwd
	}
	return ""
}

// managed reports whether the plugin may rename a tab. Herdr labels an
// unnamed tab with its position, so an all-digit label is Herdr's default; a
// label equal to the one this plugin last wrote is still ours. Anything else
// was set by hand and is left alone.
func managed(label, lastWritten string) bool {
	return label == "" || label == lastWritten || isDigits(label)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
