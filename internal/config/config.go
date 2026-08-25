// Package config reads the CLI's small set of env-var overrides. Zero-config
// by default (spec §5.2a) — every value here has a working default so the
// CLI runs with nothing set beyond what GitHub Actions itself provides.
package config

import "os"

const (
	defaultAPIURL        = "https://api.driftmapper.io"
	defaultOIDCAudience  = "https://driftmapper.io" // matches server's OIDC_AUDIENCE default
	defaultBuildInfoFile = "build-info.html"
)

// APIURL is the Driftmapper API's base URL.
func APIURL() string {
	return orDefault("DRIFTMAPPER_API_URL", defaultAPIURL)
}

// OIDCAudience is the `aud` claim requested from the CI provider's OIDC
// token endpoint. Must match the server's expected audience (internal/oidc's
// OIDC_AUDIENCE) or verification fails — overridable for non-default server
// deployments, never needed in the common case.
func OIDCAudience() string {
	return orDefault("DRIFTMAPPER_OIDC_AUDIENCE", defaultOIDCAudience)
}

// BuildInfoFile is the default output path (spec §2.4) — overridable via
// env var or the CLI's --output flag, in that precedence order reversed
// (flag wins; see cmd/driftmapper/main.go).
func BuildInfoFile() string {
	return orDefault("DRIFTMAPPER_BUILD_INFO_FILE", defaultBuildInfoFile)
}

// DashboardURL is the SPA dashboard's base origin, used only to construct
// `driftmapper compare`'s SPA compare-view URL (DRFT-36/DRFT-50). Deliberately
// has no default: unlike the API and OIDC audience, no dashboard deployment
// origin has been decided anywhere in this org yet (driftmapper/static's
// apps/dashboard has no production deploy target as of this writing). Returns
// "" when unset; callers must treat that as "compare is unusable" rather than
// guessing a host.
func DashboardURL() string {
	return os.Getenv("DRIFTMAPPER_DASHBOARD_URL")
}

// Challenge is the single-use repository-authorization value issued by the
// dashboard (spec §4.5, DRFT-61) and presented to `POST
// /v1/repositories/authorize` (DRFT-66). Deliberately has no default and is
// read only from the environment, never a flag — flags land in process
// listings and CI logs, and this is a bearer secret. Empty means "no
// challenge to redeem"; callers must not treat that as an error, since
// registration never requires one (spec §2.2a/DRFT-23).
func Challenge() string {
	return os.Getenv("DRIFTMAPPER_CHALLENGE")
}

func orDefault(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}
