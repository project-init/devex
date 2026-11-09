package errtrack

import (
	"cmp"
	"fmt"
)

const (
	// defaultSite is Datadog's US1 site, the one Datadog's own tools use when
	// DD_SITE is not set.
	defaultSite = "datadoghq.com"

	siteEnv = "DD_SITE"
	// datadogSiteEnv is the older spelling, accepted so a shell set up for
	// another Datadog tool keeps working.
	datadogSiteEnv = "DATADOG_SITE"
)

// datadogBaseURL is the API host to ask: --datadog-site, then the site the
// pasted link was on, then DD_SITE, then DATADOG_SITE, then US1.
//
// A set of keys works only on its own site, so sending them to the wrong host
// fails with a 403 that reads exactly like a missing permission. The link sits
// ahead of the environment because it names the org whose issue is being
// fetched, which is more specific than a variable exported some time ago.
//
// cmp.Or returns its first non-empty argument, so the argument order is the
// precedence, and an exported-but-empty variable is skipped rather than
// winning.
func datadogBaseURL(flagSite, linkSite string, getenv func(string) string) string {
	return "https://api." + cmp.Or(flagSite, linkSite, getenv(siteEnv), getenv(datadogSiteEnv), defaultSite)
}

// siteConflict reports when a pasted link is on one site and the environment
// names another, which datadogBaseURL resolves in the link's favour.
//
// Worth saying out loud: the keys in the environment most likely belong to the
// org the environment names, so following the link can 403 while following the
// environment would fetch the wrong org's issue. Neither choice is right, and
// a silent pick turns into an inexplicable failure. It returns "" when there is
// nothing to report, so a caller can print it unconditionally.
//
// An explicit --datadog-site settles the question, so no conflict is reported
// when one is given.
func siteConflict(flagSite, linkSite string, getenv func(string) string) string {
	if flagSite != "" || linkSite == "" {
		return ""
	}

	envSite := cmp.Or(getenv(siteEnv), getenv(datadogSiteEnv))
	if envSite == "" || envSite == linkSite {
		return ""
	}

	return fmt.Sprintf("note: the link is on %s but the environment names %s; using %s. Pass --datadog-site to choose.",
		linkSite, envSite, linkSite)
}
