package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/driftmapper/cli/internal/apiclient"
	"github.com/driftmapper/cli/internal/config"
	"github.com/driftmapper/cli/internal/oidcclient"

	"github.com/driftmapper/protocol"
)

// acquireToken gets the workload OIDC token `record` authenticates with. A package
// variable so tests do not need a CI provider; the real one requires a job that
// requested `id-token: write`, the same credential registration uses.
var acquireToken = func(ctx context.Context) (string, error) {
	if os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") == "" {
		return "", errors.New("deployments are recorded from CI only: no GitHub Actions OIDC token is available here (the job needs `permissions: id-token: write`)")
	}
	token, err := oidcclient.AcquireGitHubActionsToken(ctx, config.OIDCAudience())
	if err != nil {
		return "", fmt.Errorf("acquire OIDC token: %w", err)
	}
	return token, nil
}

// runRecord implements `driftmapper record` (DRFT-185): from a CI job, record that a
// build is now in an environment. It is a claim by the pipeline, not a check of what
// is running, and the server sets the time. Exit code 0 means recorded (or already
// recorded by this run), 1 a failure, 2 a usage error, so it works as a pipeline step.
//
// Which build: the module's registered build at the token's commit by default (right
// when the deploy runs in the same workflow as the build). --commit or --build-id name
// the build when the deploy job's own commit is not the one being deployed (a
// promotion, a workflow_dispatch, a workflow_run). --build-id is the build_instance_id
// registration printed and wrote to build-info.html. The environment may be omitted
// when the job targets a CI environment: the server takes it from the token.
//
// The one command takes one module. A monorepo deploying several runs it once each.
func runRecord(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	environment := fs.String("environment", "", "environment the build is now in (default: $DRIFTMAPPER_ENVIRONMENT, or the CI job's environment)")
	app := fs.String("app", "", "app (default: $DRIFTMAPPER_APP, or the repository name)")
	module := fs.String("module", "", "module within the app (default: $DRIFTMAPPER_MODULE, or the app name)")
	commit := fs.String("commit", "", "commit of the build being deployed (default: $DRIFTMAPPER_COMMIT, or the job's own commit)")
	buildID := fs.String("build-id", "", "exact build to record, as printed at registration (default: $DRIFTMAPPER_BUILD_ID); exclusive with --commit")
	url := fs.String("url", "", "https URL where this deployment serves its build-info (default: $DRIFTMAPPER_DEPLOYMENT_URL)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: driftmapper record [--environment=<name>] [--app=<name>] [--module=<name>] [--commit=<sha> | --build-id=<id>] [--url=<https url>]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	req := deploymentRequest(*environment, *app, *module, *commit, *buildID, *url)
	if req.BuildInstanceId != nil && req.CommitSha != nil {
		fmt.Fprintln(stderr, "driftmapper: --build-id and --commit are mutually exclusive")
		return 2
	}

	token, err := acquireToken(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "driftmapper:", err)
		return 1
	}
	dep, created, err := apiclient.New(config.APIURL(), token).RecordDeployment(ctx, req)
	if err != nil {
		fmt.Fprintln(stderr, "driftmapper:", recordDeploymentError(err))
		return 1
	}

	verb := "Recorded"
	if !created {
		verb = "Already recorded (same run)"
	}
	fmt.Fprintf(stdout, "%s %s/%s %s in %s (build %s)\n", verb, dep.App, dep.Module, shortSHA(dep.CommitSha), dep.Environment, dep.BuildInstanceId)
	return 0
}

// deploymentRequest builds the request from flags, falling back to the environment
// variables for each. Anything unset is omitted so the server applies its defaults.
func deploymentRequest(environment, app, module, commit, buildID, url string) protocol.DeploymentRequest {
	pick := func(flagValue, fallback string) *string {
		if flagValue == "" {
			flagValue = fallback
		}
		if flagValue == "" {
			return nil
		}
		return &flagValue
	}
	return protocol.DeploymentRequest{
		Environment:     pick(environment, config.Environment()),
		App:             pick(app, config.App()),
		Module:          pick(module, config.Module()),
		CommitSha:       pick(commit, config.Commit()),
		BuildInstanceId: pick(buildID, config.BuildID()),
		Url:             pick(url, config.DeploymentURL()),
	}
}

// recordDeploymentError wraps a RecordDeployment failure, adding the next step for the
// failures a first deploy step is most likely to hit. Everything else wraps generically.
func recordDeploymentError(err error) error {
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("record deployment: %w", err)
	}
	switch apiErr.Code {
	case "build_not_registered":
		return fmt.Errorf("record deployment: %s — the build job registers it (run `driftmapper` there first); if this job's commit is not the commit being deployed, pass --commit or --build-id", apiErr.Message)
	case "no_live_policy":
		return fmt.Errorf("record deployment: %s — add this repository from the dashboard (\"Add a repository\") and set DRIFTMAPPER_CHALLENGE on a registration run, then re-run", apiErr.Message)
	case "claim_mismatch":
		if apiErr.Details["mismatched_claim"] == "module" {
			return fmt.Errorf("record deployment: %w — only the repository that registered a module can record deployments of it", apiErr)
		}
	}
	return fmt.Errorf("record deployment: %w", err)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
