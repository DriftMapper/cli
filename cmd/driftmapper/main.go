// Command driftmapper is the MVP CLI's one job (spec §5.2a): acquire a
// workload OIDC token, register a build, and write build-info.html from the
// response. Write-only — it reads nothing back. No subcommands: read
// commands are deliberately deferred (spec §5.2a), so there is exactly one
// action to dispatch to.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/driftmapper/cli/internal/apiclient"
	"github.com/driftmapper/cli/internal/buildcontext"
	"github.com/driftmapper/cli/internal/buildinfo"
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

func main() {
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
