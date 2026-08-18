package handoff

import (
	"os"
	"path/filepath"
	"strings"
)

// Location is where a session ran. Root is the checkout it sat in and Repo the
// repository behind it, so sibling worktrees keep distinct Roots while sharing
// one Repo. Root is where the session was at its last event, which is not
// necessarily where it started: a session can move between worktrees, and a
// worktree can be deleted after the fact. Both git fields are empty outside a
// repository.
type Location struct {
	CWD    string
	Root   string
	Repo   string
	Branch string
}

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

// maxWalk stops a symlink cycle from hanging a hook that runs every turn.
const maxWalk = 64

// Locate resolves a working directory. It walks up for .git rather than
// spawning git, which runs in microseconds instead of milliseconds and works
// with no git on PATH. A directory outside any repository is not an error.
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
			gitDir := resolveGitDir(gitPath, info.IsDir(), dir)
			loc.Repo = repoOf(gitDir, dir)
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

// resolveGitDir returns the directory holding this checkout's HEAD. For a
// linked worktree .git is a file pointing elsewhere.
func resolveGitDir(gitPath string, isDir bool, base string) string {
	if isDir {
		return gitPath
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
		if !ok {
			continue
		}
		target := strings.TrimSpace(rest)
		if target == "" {
			return ""
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		return filepath.Clean(target)
	}
	return ""
}

// repoOf finds the checkout that sibling worktrees share, by reading the
// commondir file the way git itself does rather than pattern-matching the path.
// Layouts differ more than they look: a bare repository sitting directly at the
// project root has worktrees in <project>/worktrees/<name> with no .git
// anywhere above them, so splitting the path on "worktrees" would climb one
// level too far and group every project on the machine together.
func repoOf(gitDir, base string) string {
	if gitDir == "" {
		return ""
	}

	common := gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		if target := strings.TrimSpace(string(data)); target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(gitDir, target)
			}
			common = filepath.Clean(target)
		}
	}

	switch {
	case strings.HasPrefix(filepath.Base(common), "."):
		// .git, or a bare repo conventionally named .bare.
		return filepath.Dir(common)
	case strings.Contains(common, string(filepath.Separator)+".git"+string(filepath.Separator)):
		// A submodule's gitdir lives under the superproject's .git. The
		// submodule is its own repository, not part of the superproject.
		return base
	default:
		// A bare repository at the project root.
		return common
	}
}

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
		return "" // detached
	}
	return ref
}
