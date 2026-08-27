// Command driftmapper's default action (no subcommand) registers a build
// and writes build-info.html from the response. Write-only — it reads
// nothing back. DRFT-124/DRFT-129: there are now two producers, chosen
// automatically per the "smart enough" principle (see run's own doc
// comment) — every existing pinned CI invocation keeps calling it exactly
// as before, so that path must never change shape.
//
// When $DRIFTMAPPER_CHALLENGE is set (spec §4.5, DRFT-66), the CI producer
// first redeems it — binding the repository to an org — before
// registering. This is still a write, not a read: see maybeAuthorize and
// its own doc comment for why it's folded into register rather than a
// separate command, and how its failure modes are handled. The declared
// producer (a human-authenticated laptop, no CI) has no equivalent: org
// membership itself is the authorization, resolved from `driftmapper
// login` plus DRIFTMAPPER_ORG (see resolveOrg).
//
// `compare` (spec DRFT-50) is a pure browser launcher for the SPA compare
// view (DRFT-29); `login`/`logout` (DRFT-30) manage the declared
// producer's stored credential. All three are dispatched before the
// default action's own flag.Parse() ever runs, so none can collide with
// its flags (-output/-version/-json) — see runCompare/runLogin/runLogout
// and internal/compare's/internal/deviceauth's doc comments for why each
// performs no other network calls than the ones its name implies.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/driftmapper/cli/internal/apiclient"
	"github.com/driftmapper/cli/internal/browser"
	"github.com/driftmapper/cli/internal/buildcontext"
	"github.com/driftmapper/cli/internal/buildinfo"
	"github.com/driftmapper/cli/internal/compare"
	"github.com/driftmapper/cli/internal/config"
	"github.com/driftmapper/cli/internal/deviceauth"
	"github.com/driftmapper/cli/internal/oidcclient"

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
	// Dispatched before flag.Parse() below, so a subcommand can never
	// collide with the default action's own flags (-output/-version/-json),
	// which are all single-token and never equal to a bare subcommand name.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "compare":
			os.Exit(runCompare(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
		case "login":
			os.Exit(runLogin(context.Background(), os.Stdout, os.Stderr))
		case "logout":
			os.Exit(runLogout(os.Stdout, os.Stderr))
		}
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

// run dispatches to the CI (verified) or declared producer per DRFT-129's
// "smart enough" principle: OIDC when ACTIONS_ID_TOKEN_REQUEST_URL is set
// (a workload token is only ever obtainable inside a job that requested
// `id-token: write` — its presence is a reliable "we're in CI" signal, not
// a guess), the stored device-code credential otherwise. Both producers
// converge on the same output: a Build response written to build-info.html
// via buildinfo.Generate.
func run(ctx context.Context, output string) error {
	if output == "" {
		output = config.BuildInfoFile()
	}
	if os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != "" {
		return runVerified(ctx, output)
	}
	return runDeclared(ctx, output)
}

// runVerified is the CI producer — unchanged in shape from before
// DRFT-129, except buildcontext.FromGitHubActions now also submits
// repository/ref (confirmed against the token's claims server-side, never
// trusted from here — see that package's doc comment).
func runVerified(ctx context.Context, output string) error {
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
	return writeBuildInfo(output, build, created)
}

// runDeclared is the laptop producer (DRFT-124/DRFT-129): a human-
// authenticated write with no CI at all. Requires a prior `driftmapper
// login` — see deviceauth.AccessToken's ErrNotLoggedIn for the error
// surfaced here when that hasn't happened yet.
func runDeclared(ctx context.Context, output string) error {
	token, err := deviceauth.AccessToken(ctx, config.HubURL())
	if err != nil {
		return err
	}

	reg, err := buildcontext.FromGit()
	if err != nil {
		return fmt.Errorf("gather build context: %w", err)
	}

	client := apiclient.New(config.APIURL(), token)
	orgSlug, err := resolveOrg(ctx, client)
	if err != nil {
		return err
	}

	idempotencyKey, err := newIdempotencyKey()
	if err != nil {
		return fmt.Errorf("generate idempotency key: %w", err)
	}

	build, created, err := client.RegisterDeclaredBuild(ctx, orgSlug, idempotencyKey, reg)
	if err != nil {
		return fmt.Errorf("register build: %w", err)
	}
	return writeBuildInfo(output, build, created)
}

func writeBuildInfo(output string, build protocol.Build, created bool) error {
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

// resolveOrg picks which org a declared registration attributes to:
// DRIFTMAPPER_ORG if set, the caller's sole org if they belong to exactly
// one, or a clear "pick one" error listing every slug otherwise — guessing
// wrong here would silently attribute a build to the wrong team, so an
// ambiguous case is always an error, never a default.
func resolveOrg(ctx context.Context, client *apiclient.Client) (string, error) {
	if slug := config.Org(); slug != "" {
		return slug, nil
	}
	orgs, err := client.ListOrgs(ctx)
	if err != nil {
		return "", fmt.Errorf("list orgs: %w", err)
	}
	switch len(orgs) {
	case 0:
		return "", fmt.Errorf("your account has no organizations to register a build against")
	case 1:
		return orgs[0].Slug, nil
	default:
		slugs := make([]string, len(orgs))
		for i, o := range orgs {
			slugs[i] = o.Slug
		}
		return "", fmt.Errorf("you belong to more than one organization — set DRIFTMAPPER_ORG to one of: %v", slugs)
	}
}

// newIdempotencyKey mints a per-invocation retry key for
// RegisterDeclaredBuild (spec §2.5a never applied to this producer — see
// that method's doc comment). 16 random bytes is far more than needed to
// avoid collision within one org's history; the shape doesn't matter since
// callers must treat it as opaque either way.
func newIdempotencyKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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

// runLogin implements `driftmapper login` (DRFT-30): the declared
// producer's device-code pairing. See internal/deviceauth.Login for the
// actual flow; this just wires it to stdout/the real browser launcher and
// maps the result to an exit code.
func runLogin(ctx context.Context, stdout, stderr io.Writer) int {
	if err := deviceauth.Login(ctx, config.HubURL(), browserOpen, stdout); err != nil {
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 1
	}
	return 0
}

// runLogout implements `driftmapper logout`: deletes the stored device-code
// credential. Idempotent — logging out twice, or logging out having never
// logged in, are both just "nothing to do" (see deviceauth.Clear).
func runLogout(stdout, stderr io.Writer) int {
	if err := deviceauth.Clear(); err != nil {
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Logged out.")
	return 0
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
