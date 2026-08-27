// Package gitcontext reads build identity directly from the local git
// checkout — repository, ref, commit SHA, and commit author/committer.
// DRFT-124/DRFT-129: the CLI is no longer purely CI-driven, so this can no
// longer come only from a CI provider's environment (buildcontext.FromGitHubActions)
// or from verified OIDC claims server-side. Every producer — CI or a bare
// laptop — now goes through this package first.
//
// Shells out to the `git` binary rather than a Go git library, matching
// this repo's stdlib-only, zero-dependency posture (see CLAUDE.md's
// "public dependency graph" reasoning) — the same tradeoff
// buildcontext.FromGitHubActions' commitIdentity already made for GitHub
// Actions specifically; this package generalizes it to every provider.
package gitcontext

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Context is everything this package can determine from the local git
// checkout. Repository is empty when the remote can't be parsed into
// "owner/name" (no "origin" remote, or a URL shape this package doesn't
// recognize) — callers decide whether that's fatal.
type Context struct {
	Repository      string // "owner/name", parsed from the origin remote
	Ref             string // e.g. "refs/heads/main"; falls back to the bare commit SHA when HEAD is detached
	CommitSHA       string
	CommitAuthor    string
	CommitCommitter string
	BuiltAt         time.Time
}

// Gather runs the handful of `git` commands needed to populate Context.
// Returns an error only when this isn't a git checkout at all (no git
// binary, or `git rev-parse HEAD` fails) — commit SHA is the one field
// every registration needs. Every other field degrades independently:
// Repository is "" if the remote can't be parsed, Ref falls back to the
// bare SHA, and author/committer are best-effort (mirroring
// buildcontext.commitIdentity's own reasoning for GitHub Actions).
func Gather() (Context, error) {
	sha, err := runGit("rev-parse", "HEAD")
	if err != nil {
		return Context{}, fmt.Errorf("not a git checkout (or git is not installed): %w", err)
	}

	ctx := Context{
		CommitSHA: sha,
		Ref:       ref(sha),
		BuiltAt:   time.Now().UTC(),
	}
	if remote, err := runGit("remote", "get-url", "origin"); err == nil {
		ctx.Repository = normalizeRemote(remote)
	}
	if author, committer, ok := commitIdentity(sha); ok {
		ctx.CommitAuthor = author
		ctx.CommitCommitter = committer
	}
	return ctx, nil
}

// ref resolves the current ref, falling back to the bare commit SHA for a
// detached HEAD (a shallow CI-style checkout, or a build run against a
// specific tag/commit rather than a branch tip).
func ref(headSHA string) string {
	if symbolic, err := runGit("symbolic-ref", "-q", "HEAD"); err == nil && symbolic != "" {
		return symbolic
	}
	return headSHA
}

// commitIdentity shells out to `git show -s --format` for author/committer
// identity — best-effort, mirroring buildcontext.commitIdentity's own
// reasoning: a missing git binary or a checkout with no history degrades
// to omitting these optional fields rather than failing the registration.
func commitIdentity(sha string) (author, committer string, ok bool) {
	out, err := runGit("show", "-s", "--format=%an <%ae>%n%cn <%ce>", sha)
	if err != nil {
		return "", "", false
	}
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		return "", "", false
	}
	return lines[0], lines[1], true
}

// normalizeRemote turns a git remote URL — SSH ("git@github.com:owner/repo.git")
// or HTTPS ("https://github.com/owner/repo.git", with or without a trailing
// ".git") — into a bare "owner/repo". Anything it doesn't recognize as one
// of those two shapes returns "" (caller-visible as an unresolved
// repository, not a guess).
func normalizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")

	switch {
	case strings.HasPrefix(remote, "git@"):
		// git@host:owner/repo
		i := strings.IndexByte(remote, ':')
		if i < 0 {
			return ""
		}
		return trimToOwnerRepo(remote[i+1:])
	case strings.Contains(remote, "://"):
		// scheme://host/owner/repo
		i := strings.Index(remote, "://")
		rest := remote[i+3:]
		j := strings.IndexByte(rest, '/')
		if j < 0 {
			return ""
		}
		return trimToOwnerRepo(rest[j+1:])
	default:
		return ""
	}
}

// trimToOwnerRepo keeps exactly the last two "/"-separated path segments,
// so an SSH alias's port form (git@host:2222/owner/repo, or a self-hosted
// GitHub Enterprise Server under a subpath) still resolves to "owner/repo"
// rather than failing outright.
func trimToOwnerRepo(path string) string {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

func runGit(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
