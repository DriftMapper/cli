package gitcontext

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/widgets.git":                  "acme/widgets",
		"git@github.com:acme/widgets":                      "acme/widgets",
		"https://github.com/acme/widgets.git":              "acme/widgets",
		"https://github.com/acme/widgets":                  "acme/widgets",
		"http://internal-git.example.com/acme/widgets.git": "acme/widgets",
		"git@github.com:2222/acme/widgets.git":             "acme/widgets", // SSH alias with a path-form port
		"https://github.com/group/acme/widgets":            "acme/widgets", // GitHub Enterprise-style subpath
		"not-a-url":                                        "",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := normalizeRemote(in); got != want {
			t.Errorf("normalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

// runInTempRepo initializes a throwaway git repo with one commit and,
// unless origin is "", an origin remote — then runs fn with the working
// directory set there, restoring it afterward.
func runInTempRepo(t *testing.T, origin string) (sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=author@example.com",
			"GIT_COMMITTER_NAME=Test Committer", "GIT_COMMITTER_EMAIL=committer@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "initial")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}

	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	out, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	sha = string(out)
	sha = sha[:len(sha)-1] // trailing newline

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return sha
}

func TestGather_PopulatesFromRealRepo(t *testing.T) {
	sha := runInTempRepo(t, "git@github.com:acme/widgets.git")

	ctx, err := Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if ctx.CommitSHA != sha {
		t.Errorf("CommitSHA = %q, want %q", ctx.CommitSHA, sha)
	}
	if ctx.Repository != "acme/widgets" {
		t.Errorf("Repository = %q, want acme/widgets", ctx.Repository)
	}
	if ctx.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q, want refs/heads/main", ctx.Ref)
	}
	if ctx.CommitAuthor != "Test Author <author@example.com>" {
		t.Errorf("CommitAuthor = %q", ctx.CommitAuthor)
	}
	if ctx.CommitCommitter != "Test Committer <committer@example.com>" {
		t.Errorf("CommitCommitter = %q", ctx.CommitCommitter)
	}
	if ctx.BuiltAt.IsZero() {
		t.Error("BuiltAt is zero")
	}
}

func TestGather_NoOriginRemote_RepositoryEmpty(t *testing.T) {
	runInTempRepo(t, "")

	ctx, err := Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if ctx.Repository != "" {
		t.Errorf("Repository = %q, want empty (no origin remote)", ctx.Repository)
	}
}

func TestGather_DetachedHEAD_RefFallsBackToSHA(t *testing.T) {
	sha := runInTempRepo(t, "")
	cmd := exec.Command("git", "checkout", "-q", "--detach", "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach: %v\n%s", err, out)
	}

	ctx, err := Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if ctx.Ref != sha {
		t.Errorf("Ref = %q, want the bare SHA %q (detached HEAD)", ctx.Ref, sha)
	}
}

func TestGather_NotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	if _, err := Gather(); err == nil {
		t.Error("Gather in a non-git directory: want error, got nil")
	}
}
