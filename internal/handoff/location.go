package handoff

import (
	"os"
	"path/filepath"
	"strings"
)

// Location is where a session was running.
//
// Two git identities rather than one, because they answer different questions.
// Root is the worktree the session sat in — under a worktree-per-task workflow
// that is the task boundary, so it is what a handoff is keyed on. Repo is the
// repository shared by every worktree of Root, which is what lets a reader
// group those tasks back under one project. Both are empty outside a
// repository: the vault is not one, and a handoff must still be written there.
type Location struct {
	CWD    string
	Root   string
	Repo   string
	Branch string
}

// Name is the short label for this location — the worktree directory when
// there is one, otherwise whatever directory the session was launched from.
func (l Location) Name() string {
	dir := l.Root
	if dir == "" {
		dir = l.CWD
	}
	dir = strings.TrimRight(dir, string(filepath.Separator))
	if dir == "" {
		return "(unknown)"
	}
	return filepath.Base(dir)
}

// maxWalk bounds the search for a .git entry. Deeper than any real checkout,
// and it means a symlink cycle cannot hang a hook that runs on every turn.
const maxWalk = 64

// Locate resolves a working directory into a Location.
//
// It walks up looking for .git rather than shelling out to git. Stop fires on
// every turn completion, so this runs constantly; a few Lstat calls cost
// microseconds where a subprocess costs milliseconds, and it keeps the binary
// working on a machine with no git on PATH.
//
// Never fails: a directory outside any repository is a perfectly good location,
// and an unreadable .git just means the git fields stay empty.
func Locate(cwd string) Location {
	loc := Location{CWD: cwd}
	if cwd == "" {
		return loc
	}

	dir := cwd
	for range maxWalk {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Lstat(gitPath)
		if err == nil {
			loc.Root = dir
			gitDir, common := resolveGitDir(gitPath, info.IsDir(), dir)
			if common != "" {
				loc.Repo = filepath.Dir(common)
			}
			loc.Branch = branchOf(gitDir)
			return loc
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return loc
}

// resolveGitDir returns the directory holding this checkout's HEAD, and the
// common directory shared with every other worktree of the same repository.
//
// For an ordinary clone both are <root>/.git. For a linked worktree, .git is a
// file pointing at <main>/.git/worktrees/<name>: HEAD lives there, but the
// common directory is <main>/.git — which is exactly what makes two worktrees
// recognisable as one repository.
func resolveGitDir(gitPath string, isDir bool, base string) (gitDir, common string) {
	if isDir {
		return gitPath, gitPath
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", ""
	}
	target := ""
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:"); ok {
			target = strings.TrimSpace(rest)
			break
		}
	}
	if target == "" {
		return "", ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	target = filepath.Clean(target)

	common = target
	// Also covers submodules, whose gitdir has no /worktrees/ segment and is
	// therefore already the common directory.
	if i := strings.Index(target, string(filepath.Separator)+"worktrees"+string(filepath.Separator)); i >= 0 {
		common = target[:i]
	}
	return target, common
}

// branchOf reads the checked-out branch out of HEAD. A detached HEAD holds a
// bare sha rather than a ref, and reports no branch rather than a meaningless
// one.
func branchOf(gitDir string) string {
	if gitDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return ref
}
