package errtrack

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"uuid"
)

// Datadog serves the Error Tracking UI at two URLs for the same issue, and
// people paste either one. Both shapes below were read off real links on
// 2026-11-08 and are not documented anywhere, so treat them as liable to
// change — especially sp, which is side-panel state the UI owns:
//
//	/error-tracking/issue/<uuid>   the full issue page, id in the path
//	/error-tracking?sp=<json>      the explorer with an issue panel open
//
// The explorer form is the more common paste, being one click earlier.
const (
	// errorTrackingPath is the explorer, and the prefix of the issue page.
	errorTrackingPath = "/error-tracking"
	// issueSegment precedes the id in the issue-page path.
	issueSegment = "issue"
	// spParam carries the explorer's side-panel state as JSON.
	spParam = "sp"
	// issuePanelKind is the sp entry whose params hold an issue id.
	issuePanelKind = "error-tracking-issue"
	// datadogSuffixes are the domains a Datadog app host ends with. The site
	// is the host minus a leading "app.", so app.datadoghq.eu is datadoghq.eu
	// and us3.datadoghq.com is itself.
	appHostPrefix = "app."
)

var datadogSuffixes = []string{".datadoghq.com", ".datadoghq.eu", ".ddog-gov.com", ".datadoghq.us"}

// Target is what a pasted link resolved to.
type Target struct {
	IssueID string // the Error Tracking issue, always a UUID
	Site    string // Datadog site from the link host, e.g. datadoghq.eu, and empty for bare ID
}

// ParseTarget resolves a pasted Error Tracking link, or a bare issue id, to the
// issue the API needs and the Datadog site to ask.
//
// It fails rather than guessing. A link that cannot be resolved here would
// otherwise reach Datadog as a malformed request and come back as a 404, which
// reads like the issue was deleted.
func ParseTarget(raw string) (Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, fmt.Errorf("no link given: paste an %s issue URL or a bare issue id", errorTrackingPath)
	}

	// A bare id carries no host, so it leaves Site empty and the caller falls
	// back to --datadog-site or the environment.
	if err := validID(raw); err == nil {
		return Target{IssueID: raw}, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("not a URL or an issue id: %q", raw)
	}

	site, err := siteOf(u.Host)
	if err != nil {
		return Target{}, err
	}

	id, err := issueIDOf(u)
	if err != nil {
		return Target{}, err
	}

	return Target{IssueID: id, Site: site}, nil
}

// issueIDOf reads the issue id from whichever of the two URL shapes u is.
func issueIDOf(u *url.URL) (string, error) {
	path := strings.TrimSuffix(u.Path, "/")

	if path != errorTrackingPath && !strings.HasPrefix(path, errorTrackingPath+"/") {
		return "", fmt.Errorf("not an Error Tracking link: expected a %s path, got %q", errorTrackingPath, u.Path)
	}

	// Issue page: /error-tracking/issue/<uuid>.
	if segments := strings.Split(strings.Trim(path, "/"), "/"); len(segments) == 3 && segments[1] == issueSegment {
		id := segments[2]
		if err := validID(id); err != nil {
			return "", fmt.Errorf("%q in the link path is not an issue id: %w", id, err)
		}

		return id, nil
	}

	// Explorer: the id lives in the side-panel state.
	return issueIDFromPanels(u.Query().Get(spParam))
}

// issueIDFromPanels reads the issue id out of the explorer's sp parameter,
// which holds a JSON array of open side panels.
func issueIDFromPanels(sp string) (string, error) {
	if sp == "" {
		return "", fmt.Errorf("that link has no issue selected: open the issue and copy the URL again")
	}

	// Only the two fields that matter: i is the panel kind, p its params.
	var panels []struct {
		Kind   string `json:"i"`
		Params struct {
			IssueID string `json:"issueId"`
		} `json:"p"`
	}
	if err := json.Unmarshal([]byte(sp), &panels); err != nil {
		// sp is undocumented UI state, so a shape change lands here rather
		// than silently yielding no id.
		return "", fmt.Errorf("could not read the %s parameter of that link, Datadog may have changed its format: %w", spParam, err)
	}

	// Range rather than taking panels[0]: sp is an array because the UI stacks
	// panels, so the issue is not always first.
	for _, p := range panels {
		if p.Kind != issuePanelKind {
			continue
		}
		if err := validID(p.Params.IssueID); err != nil {
			return "", fmt.Errorf("%q in the link's %s parameter is not an issue id: %w", p.Params.IssueID, spParam, err)
		}

		return p.Params.IssueID, nil
	}

	return "", fmt.Errorf("that link has a side panel open, but not an issue one: open the issue and copy the URL again")
}

// siteOf turns a Datadog app host into the site to build an API host from.
func siteOf(host string) (string, error) {
	host = strings.ToLower(host)
	if i := strings.LastIndex(host, ":"); i != -1 && !strings.Contains(host[i:], "]") {
		host = host[:i] // drop a port
	}

	for _, suffix := range datadogSuffixes {
		if strings.HasSuffix(host, suffix) {
			return strings.TrimPrefix(host, appHostPrefix), nil
		}
	}

	return "", fmt.Errorf("%q is not a Datadog host: paste a link from the Datadog app", host)
}

// validID reports whether s is an issue id. Datadog issue ids are UUIDs, so
// checking here turns a mangled link into a clear error instead of a 404.
func validID(s string) error {
	_, err := uuid.Parse(s)

	return err
}
