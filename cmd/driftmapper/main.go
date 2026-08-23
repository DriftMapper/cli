// Command driftmapper's default action (no subcommand) is the MVP CI job
// (spec §5.2a): acquire a workload OIDC token, register a build, and write
// build-info.html from the response. Write-only — it reads nothing back,
// and every existing pinned CI invocation calls it exactly this way, so
// that path must never change shape.
//
// When $DRIFTMAPPER_CHALLENGE is set (spec §4.5, DRFT-66), the default
// action first redeems it — binding the repository to an org — before
// registering. This is still a write, not a read: see maybeAuthorize and
// its own doc comment for why it's folded into register rather than a
// separate command, and how its failure modes are handled.
//
// `compare` (spec DRFT-50) is the one read subcommand: an unauthenticated
// browser launcher for the SPA compare view (DRFT-29), dispatched on before
// the default action's own flag.Parse() ever runs — see runCompare and
// internal/compare's doc comment for why it performs no network calls of
// its own.
//
// `deploy` (spec's deploy-marking design, DRFT-81/82/88) is a second write
// subcommand, dispatched the same way: acquire a workload OIDC token and
// call RecordDeployment. Deliberately a separate, explicit CI step from the
// default action rather than folded in — see runDeploy's doc comment.
//
// `verify` (DRFT-98, DRFT-101's opinionated verification) is a third
// subcommand: resolve a deployment by its ledger-row ID, fetch the URL
// that deployment recorded, parse the deployed build-info.html meta tags
// with internal/buildinfo's parser, compare what was found against what
// the deployment claims, and record the outcome via RecordVerification —
// whatever the outcome was, including fetch/parse failures and mismatches.
// DriftMapper-the-service still checks nothing itself (DRFT-27): the
// fetch lives here, in the customer's own CI. See runVerify's doc comment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/driftmapper/cli/internal/apiclient"
	"github.com/driftmapper/cli/internal/browser"
	"github.com/driftmapper/cli/internal/buildcontext"
	"github.com/driftmapper/cli/internal/buildinfo"
	"github.com/driftmapper/cli/internal/compare"
	"github.com/driftmapper/cli/internal/config"
	"github.com/driftmapper/cli/internal/oidcclient"
	"github.com/driftmapper/cli/internal/sitefetch"
	"github.com/driftmapper/protocol"
)

// version is overwritten via -ldflags at release time; DRFT-19 wires that
// up alongside npm distribution. "dev" is correct for local/source builds.
var version = "dev"

// name is a permanent compatibility contract with npm/wrapper/lib/resolve.js's
// PATH-fallback tier: `--version --json` is how the wrapper decides whether a
// binary it found on PATH is actually this CLI (identifiesAsDriftmapper()).
// Rename this, or change the JSON shape incompatibly, only in lockstep with
// resolve.js's NAME constant — otherwise a newer wrapper silently stops
// recognizing an older binary on PATH. See CLAUDE.md.
const name = "driftmapper"

// browserOpen is a package-level indirection to browser.Open, swapped out
// in tests so `compare -open` doesn't spawn a real browser process.
var browserOpen = browser.Open

func main() {
	// Dispatched before flag.Parse() below, so "compare" can never collide
	// with the default action's own flags (-output/-version/-json), which
	// are all single-token and never equal to a bare subcommand name.
	if len(os.Args) > 1 && os.Args[1] == "compare" {
		os.Exit(runCompare(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "deploy" {
		os.Exit(runDeploy(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		os.Exit(runVerify(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}

	output := flag.String("output", "", "path to write build-info.html (default: $DRIFTMAPPER_BUILD_INFO_FILE, or build-info.html)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	asJSON := flag.Bool("json", false, "with -version, print {\"name\",\"version\"} as JSON instead of plain text")
	flag.Parse()

	if *showVersion {
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}{name, version})
			return
		}
		fmt.Println(version)
		return
	}

	if err := run(context.Background(), *output); err != nil {
		fmt.Fprintln(os.Stderr, "driftmapper:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, output string) error {
	token, err := oidcclient.AcquireGitHubActionsToken(ctx, config.OIDCAudience())
	if err != nil {
		return fmt.Errorf("acquire OIDC token: %w", err)
	}

	reg, err := buildcontext.FromGitHubActions()
	if err != nil {
		return fmt.Errorf("gather build context: %w", err)
	}

	client := apiclient.New(config.APIURL(), token)
	if err := maybeAuthorize(ctx, os.Stdout, client, config.Challenge()); err != nil {
		return err
	}

	build, created, err := client.RegisterBuild(ctx, reg)
	if err != nil {
		return registerBuildError(err)
	}

	if output == "" {
		output = config.BuildInfoFile()
	}
	if err := buildinfo.Generate(output, build); err != nil {
		return fmt.Errorf("write %s: %w", output, err)
	}

	verb := "Registered"
	if !created {
		verb = "Already registered (idempotent retry)"
	}
	fmt.Printf("%s build %s -> %s\n", verb, build.BuildInstanceId, output)
	return nil
}

// registerBuildError wraps a RegisterBuild failure. DRFT-80: registration
// now requires a live trusted-workload policy, so a repository that was
// never authorized (or whose DRIFTMAPPER_CHALLENGE never ran) hits this
// case on every build until it is. Every other error wraps generically,
// unchanged from before — only no_live_policy gets actionable guidance,
// phrased in the dashboard's own vocabulary ("Add a repository" /
// "Authorize a repository" — it never surfaces the word "challenge" to
// users, so this message doesn't either.
func registerBuildError(err error) error {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) && apiErr.Code == "no_live_policy" {
		return fmt.Errorf("register build: %s — add this repository from the dashboard (\"Add a repository\") and set DRIFTMAPPER_CHALLENGE, then re-run", apiErr.Message)
	}
	return fmt.Errorf("register build: %w", err)
}

// maybeAuthorize redeems challenge via client.AuthorizeRepository when set,
// before register ever calls RegisterBuild (spec §4.5, DRFT-66). A no-op
// when challenge is empty — most invocations, since a repository only needs
// binding once.
//
// Folded into register rather than a separate `authorize` command
// (DRFT-66's own decision record): one CI snippet, added once and never
// edited, works identically on the first run and every run after. That
// means this makes register's first invocation do something later ones
// don't (slot consumption, policy creation) — the success message below
// exists specifically to surface that distinctly, not bury it in the
// ordinary "Registered build ..." line.
//
// Every redemption failure is fail-loud and never falls through to
// RegisterBuild, matching DRFT-66's acceptance criteria — including
// `invalid_challenge`, which the server also returns for "this challenge
// was already redeemed," identically to a genuinely bad value (spec
// §4.5's anti-enumeration rule: never let a caller distinguish the two).
// That would make a challenge secret left in place after a successful
// bind fail every subsequent run — except DRFT-74 makes the server side
// of that specific case idempotent (a repeat presentation of an
// already-redeemed challenge, from the same repository it originally
// bound, succeeds again rather than erroring), so in practice this
// doesn't happen. The success message below still recommends removing the
// secret — no reason to leave one around longer than needed — it's just
// no longer load-bearing for correctness the way it was before DRFT-74.
func maybeAuthorize(ctx context.Context, w io.Writer, client *apiclient.Client, challenge string) error {
	if challenge == "" {
		return nil
	}
	auth, err := client.AuthorizeRepository(ctx, challenge)
	if err != nil {
		return fmt.Errorf("authorize repository: %w", err)
	}
	fmt.Fprintf(w, "Authorized repository %s for org %s (challenge redeemed) — you can now remove DRIFTMAPPER_CHALLENGE\n",
		auth.RepositoryId, auth.OrganizationId)
	return nil
}

// runCompare implements `driftmapper compare <build-id-a> <build-id-b>`
// (spec DRFT-50): a pure browser launcher, no network calls. It always
// opens the SPA compare view (DRFT-29) — there is no other mode, so unlike
// most flags here there is nothing to gate behind one — and always prints
// the URL it built, so it's still usable if the launch fails or the
// environment has no browser (e.g. a headless/SSH context). Exit code 2 is
// reserved for usage/config errors; a successful launch (browser opened or
// not) is always 0, since this command no longer computes any diff result
// of its own to report via exit code.
func runCompare(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	labelA := fs.String("a-url", "", "optional display label for the first build (e.g. its deployed URL)")
	labelB := fs.String("b-url", "", "optional display label for the second build (e.g. its deployed URL)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: driftmapper compare [-a-url=<label>] [-b-url=<label>] <build-instance-id-a> <build-instance-id-b>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}

	dashboardURL := config.DashboardURL()
	if dashboardURL == "" {
		fmt.Fprintln(stderr, "driftmapper: compare requires DRIFTMAPPER_DASHBOARD_URL to be set (no default dashboard origin exists yet)")
		return 2
	}

	result := compare.Result{IDA: fs.Arg(0), IDB: fs.Arg(1), LabelA: *labelA, LabelB: *labelB}
	compareURL, err := result.OpenURL(dashboardURL)
	if err != nil {
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 2
	}

	fmt.Fprintf(stdout, "Opening comparison: %s\n", compareURL)
	if err := browserOpen(compareURL); err != nil {
		fmt.Fprintln(stderr, "driftmapper: could not open browser automatically:", err)
	}
	return 0
}

// runDeploy implements `driftmapper deploy -env=<name> [-commit=<sha>]
// [-best-effort]` (spec's deploy-marking design, DRFT-81/82/88/92):
// acquires a workload OIDC token the same way the default action does —
// this is a CI-originated write, same trust model as register — and calls
// RecordDeployment, which resolves -commit to the build the server
// registered for it (DRFT-92: the opaque build_instance_id generally can't
// reach a deploy step, but the commit always can).
//
// Deliberately a second, explicit CI step rather than folded into register
// the way challenge redemption was (DRFT-66's own decision record): that
// precedent doesn't apply here, since a deploy event happens on every real
// deploy, not once per repository. There is also, deliberately, no
// auto-invocation of deploy from the default action — someone has to add a
// second CI step that actually fires it (DRFT-81's named friction).
//
// -best-effort turns a failure into a warning on stderr and exit 0 instead
// of the default exit 1 — for a deploy pipeline that would rather ship and
// lose a drift-detection data point than fail the whole job over a
// Driftmapper outage. Unset (the default) treats a failed deploy record as
// a real failure, on the theory that most callers do want to know.
func runDeploy(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	env := fs.String("env", "", "environment this build was deployed to, e.g. production (required)")
	commit := fs.String("commit", os.Getenv("GITHUB_SHA"), "commit being deployed (default: $GITHUB_SHA — set explicitly when the deploy workflow's $GITHUB_SHA isn't the built commit, e.g. a tag- or dispatch-triggered deploy)")
	bestEffort := fs.Bool("best-effort", false, "on failure, warn on stderr and exit 0 instead of exiting 1")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: driftmapper deploy -env=<name> [-commit=<sha>] [-best-effort]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *env == "" || *commit == "" {
		fs.Usage()
		return 2
	}

	if err := doDeploy(ctx, stdout, *commit, *env); err != nil {
		if *bestEffort {
			fmt.Fprintln(stderr, "driftmapper: (best-effort, continuing) "+err.Error())
			return 0
		}
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 1
	}
	return 0
}

// doDeploy acquires an OIDC token and calls recordDeployment — split out of
// runDeploy so -best-effort's exit-0-on-failure applies uniformly to every
// failure mode (OIDC acquisition included), not just a RecordDeployment
// error.
func doDeploy(ctx context.Context, stdout io.Writer, commitSHA, environment string) error {
	token, err := oidcclient.AcquireGitHubActionsToken(ctx, config.OIDCAudience())
	if err != nil {
		return fmt.Errorf("acquire OIDC token: %w", err)
	}

	client := apiclient.New(config.APIURL(), token)
	return recordDeployment(ctx, stdout, client, commitSHA, environment)
}

// recordDeployment calls RecordDeployment and prints a confirmation to w,
// matching run()'s own "%s build %s -> %s\n" style rather than inventing a
// new output convention for a second write command. It names the
// build_instance_id commit resolved to, so that resolution is visible in
// the CI log even though the caller only ever passed a commit.
func recordDeployment(ctx context.Context, w io.Writer, client *apiclient.Client, commitSHA, environment string) error {
	deployment, created, err := client.RecordDeployment(ctx, commitSHA, environment)
	if err != nil {
		return deployError(err)
	}
	verb := "Recorded"
	if !created {
		verb = "Already recorded (idempotent retry)"
	}
	fmt.Fprintf(w, "%s build %s -> deployed to %s\n", verb, deployment.BuildInstanceId, deployment.Environment)
	return nil
}

// deployError wraps a RecordDeployment failure. Mirrors registerBuildError's
// shape: no_live_policy gets the same actionable dashboard guidance register
// itself reports for the same reason. not_found is new here — a commit
// with no registered build under this repository's token, the likely
// first-run mistake (deploying a commit whose build step never ran, or ran
// against a different repository token) — and gets its own actionable
// message rather than wrapping generically. Every other code DRFT-92
// documents (validation on a malformed environment name, claim_mismatch)
// wraps generically, since the server's own message is already specific
// enough to act on.
func deployError(err error) error {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "no_live_policy":
			return fmt.Errorf("record deployment: %s — add this repository from the dashboard (\"Add a repository\") and set DRIFTMAPPER_CHALLENGE, then re-run register", apiErr.Message)
		case "not_found":
			return fmt.Errorf("record deployment: %s — no build is registered for this commit under this repository; did the build step run first, against the same repository token?", apiErr.Message)
		}
	}
	return fmt.Errorf("record deployment: %w", err)
}

// runVerify implements `driftmapper verify [-url=<target>]
// [-header='Name: value']... [-best-effort] <deployment-id>`
// (DRFT-98/DRFT-101's opinionated verification): you build an artifact,
// deploy an artifact, verify a deployment — so the positional argument is
// the deployment row ID the deploy step's output emitted, and everything
// else (expected build, environment, target URL) comes from that row.
//
// The flow: acquire a workload OIDC token exactly like runDeploy does
// (same trust model as register and deploy), GetDeployment to resolve the
// handle, fetch the recorded URL via internal/sitefetch, parse the served
// meta tags via internal/buildinfo.Parse, compare against the deployment's
// expected build, and record what was found — whatever was found — via
// RecordVerification. DriftMapper-the-service still fetches nothing
// itself (DRFT-27); the fetch lives here, on infrastructure the customer
// controls, which is why this command may do it when the server must not.
//
// Outcomes and exit codes:
//
//   - observed == expected → recorded with outcome=verified, exit 0.
//   - observed != expected → recorded with outcome=mismatch, exit 3. A
//     mismatch IS the drift signal, not a tool failure: it is always
//     recorded before exiting, and -best-effort never swallows it.
//   - unreachable/non-200/unparsable content → recorded as
//     outcome=fetch_failed / parse_failed (failed observations are
//     assertions too), then exit 1 — or 0 under -best-effort, which covers
//     outages and lost data points, never drift.
//   - failures before anything could be recorded (OIDC acquisition,
//     GetDeployment) are plain operational errors: exit 1, or 0 under
//     -best-effort.
//
// -url supplies the fetch target only when the deployment has none
// recorded; when both exist the deployment's own URL wins and the flag is
// ignored with a warning — verifying expectation X at a self-chosen other
// URL would manufacture mismatches. -header repeats for authenticated
// targets (staging behind a gateway) and is stripped automatically if a
// redirect leaves the original host.
func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	urlOverride := fs.String("url", "", "fallback fetch target when the deployment has no url recorded")
	var headers headerFlags
	fs.Var(&headers, "header", "extra request header for the fetch, \"Name: value\" (repeatable; stripped on cross-origin redirects)")
	bestEffort := fs.Bool("best-effort", false, "on outage/failure, warn on stderr and exit 0 instead of exiting 1 (never applies to a mismatch)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: driftmapper verify [-url=<target>] [-header='Name: value']... [-best-effort] <deployment-id>")
		fmt.Fprintln(stderr, "\n  resolves the deployment, fetches its build-info.html, records what it found.")
		fmt.Fprintln(stderr, "  exits 3 on a mismatch — the drift signal — even under -best-effort.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	deploymentID, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil || deploymentID <= 0 {
		fmt.Fprintf(stderr, "driftmapper: deployment id %q must be a positive integer (the number `driftmapper deploy` printed)\n", fs.Arg(0))
		return 2
	}

	res, err := doVerify(ctx, stdout, stderr, deploymentID, *urlOverride, headers)
	if err != nil {
		if *bestEffort {
			fmt.Fprintln(stderr, "driftmapper: (best-effort, continuing) "+err.Error())
			return 0
		}
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 1
	}
	switch res.outcome {
	case protocol.VerificationRequestOutcomeVerified:
		return 0
	case protocol.VerificationRequestOutcomeMismatch:
		// Recorded already (doVerify prints the DRIFT line). Never swallowed.
		return 3
	default:
		if *bestEffort {
			fmt.Fprintf(stderr, "driftmapper: (best-effort, continuing) %s: %s\n", res.outcome, res.detail)
			return 0
		}
		fmt.Fprintf(stderr, "driftmapper: %s — recorded as an assertion, but the check did not pass: %s\n", res.outcome, res.detail)
		return 1
	}
}

// headerFlags collects repeatable -header values.
type headerFlags []sitefetch.Header

func (h *headerFlags) String() string {
	parts := make([]string, len(*h))
	for i, hdr := range *h {
		parts[i] = hdr.Name + ": " + hdr.Value
	}
	return strings.Join(parts, "; ")
}

func (h *headerFlags) Set(v string) error {
	name, value, found := strings.Cut(v, ":")
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if !found || name == "" || value == "" || strings.ContainsAny(name, " \t") {
		return fmt.Errorf("-header must be \"Name: value\", got %q", v)
	}
	*h = append(*h, sitefetch.Header{Name: name, Value: value})
	return nil
}

// verifyResult reports how an executed check ended. error is non-nil only
// when nothing could be recorded (token/deployment-resolution failures);
// once an observation exists it is always recorded first, and its outcome
// plus the human-readable reason travel back here for exit-code mapping.
type verifyResult struct {
	outcome protocol.VerificationRequestOutcome
	detail  string
}

// verifyFetcher constructs the HTTP fetcher verify uses. Package-level
// only so tests can wire it to their own httptest TLS server's client
// (self-signed); production always takes sitefetch.New's strict defaults.
var verifyFetcher = sitefetch.New

// doVerify runs one full check: resolve, fetch, parse, compare, record.
// Confirmation lines print in recordDeployment's "%s ... -> %s\n" house
// style; a DRIFT line prints before returning a mismatch so the signal is
// visible in the CI log regardless of exit-code handling.
func doVerify(ctx context.Context, stdout, stderr io.Writer, deploymentID int64, urlOverride string, headers headerFlags) (verifyResult, error) {
	token, err := oidcclient.AcquireGitHubActionsToken(ctx, config.OIDCAudience())
	if err != nil {
		return verifyResult{}, fmt.Errorf("acquire OIDC token: %w", err)
	}
	client := apiclient.New(config.APIURL(), token)

	deployment, err := client.GetDeployment(ctx, deploymentID)
	if err != nil {
		return verifyResult{}, getDeploymentError(deploymentID, err)
	}

	target := ""
	if deployment.Url != nil {
		target = *deployment.Url
	}
	if target == "" && urlOverride != "" {
		target = urlOverride
	}
	if target != "" && deployment.Url != nil && *deployment.Url != "" && urlOverride != "" && urlOverride != *deployment.Url {
		fmt.Fprintf(stderr, "driftmapper: ignoring -url %q — deployment %d records %q; unrecord the deployment or edit the flag if that row is wrong\n",
			urlOverride, deploymentID, *deployment.Url)
	}
	if target == "" {
		return verifyResult{}, fmt.Errorf("deployment %d has no url recorded — re-run deploy with -url pointing at its build-info.html, or pass -url here", deploymentID)
	}

	fetcher := verifyFetcher(headers)
	res, fetchErr := fetcher.Do(ctx, target)

	req := protocol.VerificationRequest{
		BuildInstanceId: deployment.BuildInstanceId,
		Environment:     deployment.Environment,
		DeploymentId:    &deploymentID,
	}
	outcome := protocol.VerificationRequestOutcomeVerified
	var observed string
	var detail string

	switch {
	case fetchErr != nil:
		outcome = protocol.VerificationRequestOutcomeFetchFailed
		detail = fetchErr.Error()
		fmt.Fprintf(stdout, "Recorded deployment %d -> fetch failed at %s (%v)\n", deploymentID, target, fetchErr)
	default:
		info, parseErr := buildinfo.Parse(res.Body)
		if parseErr != nil {
			outcome = protocol.VerificationRequestOutcomeParseFailed
			detail = parseErr.Error()
			fmt.Fprintf(stdout, "Recorded deployment %d -> unparsable content at %s (%v)\n", deploymentID, res.URL, parseErr)
			break
		}
		observed = info.BuildInstanceID
		req.ObservedBuildInstanceId = &observed
		if observed != deployment.BuildInstanceId {
			outcome = protocol.VerificationRequestOutcomeMismatch
		}
	}

	sourceURL := target
	if fetchErr == nil {
		sourceURL = res.URL // where the bytes actually came from
	}
	req.SourceUrl = &sourceURL
	req.Outcome = &outcome

	verification, created, err := client.RecordVerification(ctx, req)
	if err != nil {
		if outcome == protocol.VerificationRequestOutcomeMismatch {
			// The drift was observed but its row did not land — print the
			// signal before returning the operational failure so it can
			// never be silently lost to an API outage.
			fmt.Fprintf(stdout, "DRIFT: deployment %d -> expected build %s, found %s at %s (recording failed)\n",
				deploymentID, deployment.BuildInstanceId, observed, sourceURL)
		}
		return verifyResult{}, recordVerificationError(err)
	}

	if outcome == protocol.VerificationRequestOutcomeMismatch {
		fmt.Fprintf(stdout, "DRIFT: deployment %d -> expected build %s, found %s in %s (%s)\n",
			deploymentID, deployment.BuildInstanceId, observed, verification.Environment, sourceURL)
		return verifyResult{outcome: outcome}, nil
	}
	if outcome == protocol.VerificationRequestOutcomeVerified {
		verb := "Verified"
		if !created {
			verb = "Already verified (idempotent retry)"
		}
		fmt.Fprintf(stdout, "%s deployment %d -> build %s live in %s\n",
			verb, deploymentID, verification.BuildInstanceId, verification.Environment)
	}
	// fetch_failed / parse_failed already printed their descriptive line
	// before recording; nothing further to add.
	return verifyResult{outcome: outcome, detail: detail}, nil
}

// getDeploymentError wraps a GetDeployment failure with actionable
// guidance, mirroring deployError/verifyError's shape: no_live_policy gets
// register-style guidance; the 404 deliberately collapses unknown IDs,
// other-repository deployments, and missing verify bindings into one
// response (existence hiding), so the message names all three.
func getDeploymentError(deploymentID int64, err error) error {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "no_live_policy":
			return fmt.Errorf("resolve deployment %d: %s — add this repository from the dashboard (\"Add a repository\") and set DRIFTMAPPER_CHALLENGE, then re-run register", deploymentID, apiErr.Message)
		case "not_found":
			return fmt.Errorf("resolve deployment %d: %s — no such deployment under this repository (did its deploy step run first?), or this repository holds no verify binding toward the deployment's owner", deploymentID, apiErr.Message)
		}
	}
	return fmt.Errorf("resolve deployment %d: %w", deploymentID, err)
}

// recordVerificationError wraps a RecordVerification failure that happens
// after a check ran and produced an observation. Mirrors the other
// wrappers' shape: no_live_policy gets register-style guidance; everything
// else wraps generically, since the server's message already names
// specifics.
func recordVerificationError(err error) error {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) && apiErr.Code == "no_live_policy" {
		return fmt.Errorf("record verification: %s — add this repository from the dashboard (\"Add a repository\") and set DRIFTMAPPER_CHALLENGE, then re-run register", apiErr.Message)
	}
	return fmt.Errorf("record verification: %w", err)
}
