// Command driftmapper's default action (no subcommand) is the MVP CI job
// (spec §5.2a): acquire a workload OIDC token, register a build, and write
// build-info.html from the response. Write-only — it reads nothing back,
// and every existing pinned CI invocation calls it exactly this way, so
// that path must never change shape.
//
// `compare` (spec DRFT-26) is the one read subcommand: an unauthenticated,
// user-invoked diff of two already-deployed targets, dispatched on before
// the default action's own flag.Parse() ever runs — see runCompare and
// internal/compare's doc comment for why it stays intentionally thin.
// `-open` (DRFT-36) deep-links to the SPA compare view (DRFT-29) instead of
// printing the diff — see openCompareResult.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
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
	build, created, err := client.RegisterBuild(ctx, reg)
	if err != nil {
		return fmt.Errorf("register build: %w", err)
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

// runCompare implements `driftmapper compare <url-a> <url-b> [-json]`.
// Exit codes follow diff(1)'s convention, since CI is the primary caller
// (spec DRFT-26's open question on this): 0 the two targets are the same
// build, 1 they differ (drift), 2 usage or fetch/parse error.
func runCompare(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the result as JSON instead of human-readable text")
	open := fs.Bool("open", false, "open the SPA compare view (DRFT-29) in a browser instead of printing the diff")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: driftmapper compare [-json] [-open] <build-info-url-a> <build-info-url-b>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	if *asJSON && *open {
		fmt.Fprintln(stderr, "driftmapper: -json and -open are mutually exclusive")
		return 2
	}

	result, err := compare.Run(ctx, http.DefaultClient, fs.Arg(0), fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 2
	}

	if *open {
		if err := openCompareResult(stdout, stderr, result); err != nil {
			fmt.Fprintln(stderr, "driftmapper:", err)
			return 2
		}
	} else if *asJSON {
		json.NewEncoder(stdout).Encode(result)
	} else {
		printCompareResult(stdout, result)
	}

	if result.Match {
		return 0
	}
	return 1
}

// openCompareResult builds the SPA compare view URL for result and launches
// it in the user's browser, printing the URL either way so it's still
// usable if the launch fails or the environment has no browser (e.g. a
// headless CI runner — -open is meant for laptop use, but nothing stops it
// from being invoked there).
func openCompareResult(stdout, stderr io.Writer, result compare.Result) error {
	dashboardURL := config.DashboardURL()
	if dashboardURL == "" {
		return fmt.Errorf("-open requires DRIFTMAPPER_DASHBOARD_URL to be set (no default dashboard origin exists yet)")
	}

	compareURL, err := result.OpenURL(dashboardURL)
	if err != nil {
		return fmt.Errorf("build compare URL: %w", err)
	}

	fmt.Fprintf(stdout, "Opening comparison: %s\n", compareURL)
	if err := browserOpen(compareURL); err != nil {
		fmt.Fprintln(stderr, "driftmapper: could not open browser automatically:", err)
	}
	return nil
}

func printCompareResult(w io.Writer, r compare.Result) {
	printTarget := func(label string, t compare.Target) {
		fmt.Fprintf(w, "%s  %s\n", label, t.URL)
		fmt.Fprintf(w, "   build  %s\n", t.Info.BuildInstanceID)
		if t.Info.ResolutionURL != "" {
			fmt.Fprintf(w, "   view   %s\n", t.Info.ResolutionURL)
		}
	}
	printTarget("A", r.A)
	printTarget("B", r.B)

	if r.Match {
		fmt.Fprintln(w, "\nsame build")
		return
	}
	fmt.Fprintln(w, "\ndrift detected: different builds")
}
