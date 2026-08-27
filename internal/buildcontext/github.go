// Package buildcontext normalizes a build's context into the CLI-submitted
// half of a build registration (spec §2.2a, inverted by DRFT-129).
//
// FromGit (DRFT-129) is the base layer, shared by every producer: it reads
// repository/ref/commit/author/committer straight from the local git
// checkout via internal/gitcontext. FromGitHubActions overlays CI-only
// facts (trigger event; GITHUB_SHA as the authoritative commit, since a
// CI-provided value should win over whatever HEAD happens to resolve to in
// an unusual checkout) on top of that same base — the two producers share
// one code path for everything git itself can answer, and only genuinely
// CI-specific context is layered in separately.
//
// repository_id, visibility, workflow, run_id, and run_attempt remain
// exclusively token-derived for a verified (workload-OIDC) registration —
// the server rejects them if present in the request body (spec §2.2a) —
// so this package has no business producing them either.
package buildcontext

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/driftmapper/cli/internal/gitcontext"
	"github.com/driftmapper/protocol"
)

// FromGit builds a BuildRegistration purely from the local git checkout —
// the declared (non-CI) producer's entire build context, and the base
// layer FromGitHubActions overlays CI-only fields on top of. Fails only
// when gitcontext.Gather does (not a git checkout at all); every other
// field degrades independently — see gitcontext.Gather's own doc comment.
func FromGit() (protocol.BuildRegistration, error) {
	ctx, err := gitcontext.Gather()
	if err != nil {
		return protocol.BuildRegistration{}, err
	}
	reg := protocol.BuildRegistration{
		CommitSha: ctx.CommitSHA,
		BuiltAt:   ctx.BuiltAt,
	}
	if ctx.Repository != "" {
		reg.Repository = &ctx.Repository
	}
	if ctx.Ref != "" {
		reg.Ref = &ctx.Ref
	}
	if ctx.CommitAuthor != "" {
		reg.CommitAuthor = &ctx.CommitAuthor
	}
	if ctx.CommitCommitter != "" {
		reg.CommitCommitter = &ctx.CommitCommitter
	}
	return reg, nil
}

// FromGitHubActions builds a BuildRegistration from the current GitHub
// Actions job's environment, layered on top of FromGit's local-checkout
// base (repository/ref/author/committer — actions/checkout leaves a real
// git checkout behind, so this is almost always available). Only
// GITHUB_SHA is required — that's the one field where the CI-provided
// value must win over FromGit's own HEAD (built_at is also CI-owned: "now"
// as the job runs, not whatever timestamp FromGit's own commit carries).
// A FromGit failure (no git binary, or somehow not a checkout at all)
// degrades to the pre-DRFT-129 GITHUB_SHA-only behavior rather than
// failing the whole registration — repository/ref end up unset, exactly
// like every CLI version before this one.
func FromGitHubActions() (protocol.BuildRegistration, error) {
	sha := os.Getenv("GITHUB_SHA")
	if sha == "" {
		return protocol.BuildRegistration{}, fmt.Errorf("GITHUB_SHA is not set — not running in a GitHub Actions job?")
	}

	reg, err := FromGit()
	if err != nil {
		reg = protocol.BuildRegistration{}
	}
	reg.CommitSha = sha
	reg.BuiltAt = time.Now().UTC()
	trigger := normalizeTrigger(os.Getenv("GITHUB_EVENT_NAME"))
	reg.TriggerEvent = &trigger

	if reg.CommitAuthor == nil || reg.CommitCommitter == nil {
		if author, committer, ok := commitIdentity(sha); ok {
			reg.CommitAuthor = &author
			reg.CommitCommitter = &committer
		}
	}
	return reg, nil
}

// normalizeTrigger maps GitHub's GITHUB_EVENT_NAME onto the normalized
// trigger vocabulary spec §2.2a describes (push/pull_request/manual/...).
// GitHub's own "workflow_dispatch" is the one name that needs translating;
// every other event name is already a reasonable normalized value and is
// passed through unchanged, since v1 has exactly one provider and no
// documented enum to validate against.
func normalizeTrigger(event string) string {
	switch event {
	case "workflow_dispatch":
		return "manual"
	case "":
		return "unknown"
	default:
		return event
	}
}

// commitIdentity shells out to `git log -1` for author/committer identity,
// which GitHub Actions does not expose as a plain env var. Best-effort: a
// missing git binary or a checkout with no history (e.g. fetch-depth: 1
// pointed at the wrong ref) degrades to omitting these optional fields
// rather than failing the whole registration.
func commitIdentity(sha string) (author, committer string, ok bool) {
	out, err := exec.Command("git", "log", "-1", "--format=%an <%ae>%n%cn <%ce>", sha).Output()
	if err != nil {
		return "", "", false
	}
	lines := strings.SplitN(strings.TrimRight(string(out), "\n"), "\n", 2)
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		return "", "", false
	}
	return lines[0], lines[1], true
}
