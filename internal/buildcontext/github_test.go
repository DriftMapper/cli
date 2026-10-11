package buildcontext

import (
	"os"
	"os/exec"
	"testing"
)

func TestFromGitHubActions_RequiresGitHubSHA(t *testing.T) {
	t.Setenv("GITHUB_SHA", "")

	if _, err := FromGitHubActions(); err == nil {
		t.Error("FromGitHubActions with no GITHUB_SHA: want error, got nil")
	}
}

func TestFromGitHubActions_PopulatesCLISubmittedFieldsOnly(t *testing.T) {
	t.Setenv("GITHUB_SHA", "deadbeef")
	t.Setenv("GITHUB_EVENT_NAME", "push")

	reg, err := FromGitHubActions()
	if err != nil {
		t.Fatalf("FromGitHubActions: %v", err)
	}
	if reg.CommitSha != "deadbeef" {
		t.Errorf("CommitSha = %q, want %q", reg.CommitSha, "deadbeef")
	}
	if reg.TriggerEvent == nil || *reg.TriggerEvent != "push" {
		t.Errorf("TriggerEvent = %v, want \"push\"", reg.TriggerEvent)
	}
	if reg.BuiltAt.IsZero() {
		t.Error("BuiltAt is zero")
	}
}

func TestNormalizeTrigger(t *testing.T) {
	cases := map[string]string{
		"push":              "push",
		"pull_request":      "pull_request",
		"workflow_dispatch": "manual",
		"":                  "unknown",
		"some_future_event": "some_future_event",
	}
	for in, want := range cases {
		if got := normalizeTrigger(in); got != want {
			t.Errorf("normalizeTrigger(%q) = %q, want %q", in, got, want)
		}
	}
}

// A local build is never attributed to a repository (DRFT-165): even in a
// checkout with a git remote, ForLocalBuild submits none, while FromGit (the
// base layer the CI producer builds on, where the server confirms it) does.
func TestForLocalBuild_NeverSubmitsARepository(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("remote", "add", "origin", "https://github.com/acme/laptop-repo.git")
	t.Chdir(dir)

	base, err := FromGit()
	if err != nil {
		t.Fatalf("FromGit: %v", err)
	}
	if base.Repository == nil || *base.Repository != "acme/laptop-repo" {
		t.Fatalf("setup: FromGit repository = %v, want acme/laptop-repo (the CI producer keeps it)", base.Repository)
	}

	local, err := ForLocalBuild()
	if err != nil {
		t.Fatalf("ForLocalBuild: %v", err)
	}
	if local.Repository != nil {
		t.Errorf("ForLocalBuild submitted repository %q, want none", *local.Repository)
	}
	if local.CommitSha != base.CommitSha || local.CommitSha == "" {
		t.Errorf("ForLocalBuild commit = %q, want %q (everything else is unchanged)", local.CommitSha, base.CommitSha)
	}
}
