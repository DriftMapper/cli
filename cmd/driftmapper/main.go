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
// `compare` (spec DRFT-50) is a pure browser launcher for the SPA compare
// view (DRFT-29), dispatched on before
// the default action's own flag.Parse() ever runs — see runCompare and
// internal/compare's doc comment for why it performs no network calls of
// its own.
package main

import (
	"context"
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
	"github.com/driftmapper/cli/internal/oidcclient"
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
