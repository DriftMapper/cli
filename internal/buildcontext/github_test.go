package buildcontext

import "testing"

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
