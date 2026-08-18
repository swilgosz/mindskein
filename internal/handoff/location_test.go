package handoff

import (
	"os"
	"path/filepath"
	"testing"
)

// mkGitDir writes an ordinary .git directory with the given HEAD contents.
func mkGitDir(t *testing.T, root, head string) {
	t.Helper()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocateOrdinaryRepoFromSubdirectory(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "repo")
	sub := filepath.Join(root, "internal", "handoff")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mkGitDir(t, root, "ref: refs/heads/main\n")

	got := Locate(sub)
	if got.Root != root {
		t.Errorf("Root = %q, want %q", got.Root, root)
	}
	if got.Repo != root {
		t.Errorf("Repo = %q, want %q", got.Repo, root)
	}
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want %q", got.Branch, "main")
	}
	if got.Name() != "repo" {
		t.Errorf("Name() = %q, want %q", got.Name(), "repo")
	}
}

// TestLocateWorktree is the case the project key depends on: a worktree is its
// own Root (so two tasks in one repo stay apart) but shares Repo with the main
// checkout (so the brief can group them).
func TestLocateWorktree(t *testing.T) {
	tmp := t.TempDir()
	main := filepath.Join(tmp, "mindskein")
	wt := filepath.Join(tmp, "u2-handoff")
	mkGitDir(t, main, "ref: refs/heads/main\n")

	wtGitDir := filepath.Join(main, ".git", "worktrees", "u2-handoff")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/u2-handoff-writer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// git always writes commondir, and it is how the shared repository is found.
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Locate(wt)
	if got.Root != wt {
		t.Errorf("Root = %q, want the worktree %q", got.Root, wt)
	}
	if got.Repo != main {
		t.Errorf("Repo = %q, want the shared repo %q", got.Repo, main)
	}
	if got.Branch != "u2-handoff-writer" {
		t.Errorf("Branch = %q, want the worktree branch", got.Branch)
	}

	// The main checkout must agree about Repo, or grouping breaks.
	if mainLoc := Locate(main); mainLoc.Repo != got.Repo {
		t.Errorf("main Repo = %q, worktree Repo = %q — must match", mainLoc.Repo, got.Repo)
	}
}

// TestLocateOutsideRepo covers the vault, which is not a repository. A handoff
// must still be written for it.
func TestLocateOutsideRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "62. Business")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := Locate(dir)
	if got.Root != "" || got.Repo != "" || got.Branch != "" {
		t.Errorf("git fields should be empty outside a repo, got %+v", got)
	}
	if got.Name() != "62. Business" {
		t.Errorf("Name() = %q, want the folder name", got.Name())
	}
}

func TestLocateDetachedHeadReportsNoBranch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	mkGitDir(t, root, "3a554169b0c0ffee3a554169b0c0ffee3a554169\n")
	if got := Locate(root); got.Branch != "" {
		t.Errorf("Branch = %q, want empty for a detached HEAD", got.Branch)
	}
}

func TestLocateEmptyCWD(t *testing.T) {
	if got := Locate(""); got.Name() != "(unknown)" {
		t.Errorf("Name() = %q, want %q", got.Name(), "(unknown)")
	}
}

// mkWorktree writes a linked worktree: a .git file pointing at gitDir, which
// holds HEAD and the commondir pointer git uses to find the shared repository.
func mkWorktree(t *testing.T, root, gitDir, commondir, branch string) {
	t.Helper()
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte(commondir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLocateBareRepoAtProjectRoot covers a layout with no .git or .bare above
// the checkouts at all: the repository lives directly at the project root and
// its worktrees sit beside it. Splitting the gitdir path on "worktrees" climbs
// one level too far here and lands on the directory holding every project.
func TestLocateBareRepoAtProjectRoot(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "automateideasai.com")

	mkWorktree(t, filepath.Join(proj, "main"), filepath.Join(proj, "worktrees", "main"), "../..", "main")
	mkWorktree(t, filepath.Join(proj, "ad-banner"), filepath.Join(proj, "worktrees", "ad-banner"), "../..", "fix/ad-banner")

	first := Locate(filepath.Join(proj, "main"))
	second := Locate(filepath.Join(proj, "ad-banner"))

	if first.Repo != proj {
		t.Errorf("Repo = %q, want the project root %q", first.Repo, proj)
	}
	if first.Repo != second.Repo {
		t.Errorf("sibling worktrees disagree on Repo: %q vs %q", first.Repo, second.Repo)
	}
	if first.Root == second.Root {
		t.Error("sibling worktrees must keep distinct Roots")
	}
	if second.Branch != "fix/ad-banner" {
		t.Errorf("Branch = %q, want fix/ad-banner", second.Branch)
	}
	if first.Repo == filepath.Dir(proj) {
		t.Fatalf("Repo climbed past the project to %q — every project would group together", first.Repo)
	}
}

// TestLocateBareDirLayout covers the other bare convention, a .bare directory
// beside the worktrees.
func TestLocateBareDirLayout(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	mkWorktree(t, filepath.Join(proj, "feat"), filepath.Join(proj, ".bare", "worktrees", "feat"), "../..", "feat")

	if got := Locate(filepath.Join(proj, "feat")); got.Repo != proj {
		t.Errorf("Repo = %q, want %q", got.Repo, proj)
	}
}

// TestLocateSubmodule: a submodule's gitdir lives under the superproject's
// .git, but the submodule is its own repository. Resolving it to the
// superproject would group every submodule of a repo into one line.
func TestLocateSubmodule(t *testing.T) {
	tmp := t.TempDir()
	super := filepath.Join(tmp, "super")
	mkGitDir(t, super, "ref: refs/heads/main\n")

	var repos []string
	for _, name := range []string{"lib-a", "lib-b"} {
		sub := filepath.Join(super, name)
		gitDir := filepath.Join(super, ".git", "modules", name)
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := Locate(sub)
		if got.Repo != sub {
			t.Errorf("%s: Repo = %q, want the submodule itself %q", name, got.Repo, sub)
		}
		repos = append(repos, got.Repo)
	}
	if repos[0] == repos[1] {
		t.Errorf("both submodules resolved to %q — unrelated repos collapsed into one group", repos[0])
	}
}
