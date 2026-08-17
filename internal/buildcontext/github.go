// Package buildcontext normalizes a CI provider's build context into the
// CLI-submitted half of a build registration (spec §2.2a). Deliberately
// narrow: repository identity, ref, workflow, and run id/attempt are
// token-derived and never appear here — the server rejects them if present
// in the request body (spec §2.2a's central rule), so this package has no
// business producing them in the first place.
package buildcontext

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/driftmapper/protocol"
)

// FromGitHubActions builds a BuildRegistration from the current GitHub
// Actions job's environment. Only GITHUB_SHA is required; commit author/
// committer are best-effort (git may be unavailable, or the checkout
// shallow) and are optional fields on the wire, so a failure to resolve
// them is not fatal.
func FromGitHubActions() (protocol.BuildRegistration, error) {
	sha := os.Getenv("GITHUB_SHA")
	if sha == "" {
		return protocol.BuildRegistration{}, fmt.Errorf("GITHUB_SHA is not set — not running in a GitHub Actions job?")
	}

	trigger := normalizeTrigger(os.Getenv("GITHUB_EVENT_NAME"))
	reg := protocol.BuildRegistration{
		CommitSha:    sha,
		BuiltAt:      time.Now().UTC(),
		TriggerEvent: &trigger,
	}
	if author, committer, ok := commitIdentity(sha); ok {
		reg.CommitAuthor = &author
		reg.CommitCommitter = &committer
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
