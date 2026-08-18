// Package config reads the CLI's small set of env-var overrides. Zero-config
// by default (spec §5.2a) — every value here has a working default so the
// CLI runs with nothing set beyond what GitHub Actions itself provides.
package config

import "os"

const (
	defaultAPIURL        = "https://api.driftmapper.com"
	defaultOIDCAudience  = "https://driftmapper.com" // matches server's OIDC_AUDIENCE default
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
// `driftmapper compare -open`'s deep link (DRFT-36). Deliberately has no
// default: unlike the API and OIDC audience, no dashboard deployment origin
// has been decided anywhere in this org yet (driftmapper/static's apps/dashboard
// has no production deploy target as of this writing). Returns "" when unset;
// callers must treat that as "-open is unusable" rather than guessing a host.
func DashboardURL() string {
	return os.Getenv("DRIFTMAPPER_DASHBOARD_URL")
}

func orDefault(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}
