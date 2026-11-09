package errtrack

import (
	"strings"
	"testing"
)

// The two real links, captured from the Datadog UI on 2026-11-08. Both point at
// the same issue, which is the point: the explorer and the issue page are two
// views of one thing, and a paste can be either.
const (
	issueID = "aae10c1c-dacd-11f1-a39f-da7ad0900002"

	issuePageLink = "https://app.datadoghq.com/error-tracking/issue/" + issueID +
		"?query=&index=&from_ts=1794008193976&to_ts=1794094593976&live=true"

	explorerLink = "https://app.datadoghq.com/error-tracking" +
		"?query=&refresh_mode=sliding&source=all" +
		"&sp=%5B%7B%22p%22%3A%7B%22issueId%22%3A%22" + issueID + "%22%7D%2C%22i%22%3A%22error-tracking-issue%22%7D%5D" +
		"&from_ts=1794008193976&to_ts=1794094593976&live=true"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantID   string
		wantSite string
	}{
		{name: "issue page", raw: issuePageLink, wantID: issueID, wantSite: "datadoghq.com"},
		{name: "explorer side panel", raw: explorerLink, wantID: issueID, wantSite: "datadoghq.com"},

		// A bare id has no host, so Site stays empty and the caller falls back
		// to --datadog-site or the environment.
		{name: "bare issue id", raw: issueID, wantID: issueID},
		{name: "bare id with surrounding space", raw: "  " + issueID + "\n", wantID: issueID},

		// Query parameters are read by name, so their order and presence do not
		// matter. The original parser depended on query= coming first.
		{
			name: "issue page with reordered params", wantID: issueID, wantSite: "datadoghq.com",
			raw: "https://app.datadoghq.com/error-tracking/issue/" + issueID + "?from_ts=1&live=true",
		},
		{
			name: "issue page with no params", wantID: issueID, wantSite: "datadoghq.com",
			raw: "https://app.datadoghq.com/error-tracking/issue/" + issueID,
		},
		{
			name: "issue page with trailing slash", wantID: issueID, wantSite: "datadoghq.com",
			raw: "https://app.datadoghq.com/error-tracking/issue/" + issueID + "/",
		},

		// Site comes from the host: trim a leading app., keep everything else,
		// so an org on another Datadog site is asked on its own API host.
		{
			name: "eu site", wantID: issueID, wantSite: "datadoghq.eu",
			raw: "https://app.datadoghq.eu/error-tracking/issue/" + issueID,
		},
		{
			name: "us3 site keeps its subdomain", wantID: issueID, wantSite: "us3.datadoghq.com",
			raw: "https://us3.datadoghq.com/error-tracking/issue/" + issueID,
		},
		{
			name: "gov site", wantID: issueID, wantSite: "ddog-gov.com",
			raw: "https://app.ddog-gov.com/error-tracking/issue/" + issueID,
		},
		{
			name: "host case is ignored", wantID: issueID, wantSite: "datadoghq.com",
			raw: "https://App.DataDogHQ.com/error-tracking/issue/" + issueID,
		},

		// sp is an array because the UI stacks panels, so the issue one is not
		// always first. Taking sp[0] would pass every other case and fail here.
		{
			name: "issue panel is not the first panel", wantID: issueID, wantSite: "datadoghq.com",
			raw: `https://app.datadoghq.com/error-tracking?sp=` +
				`%5B%7B%22i%22%3A%22logs%22%2C%22p%22%3A%7B%7D%7D%2C` +
				`%7B%22p%22%3A%7B%22issueId%22%3A%22` + issueID + `%22%7D%2C%22i%22%3A%22error-tracking-issue%22%7D%5D`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTarget(tt.raw)
			if err != nil {
				t.Fatalf("ParseTarget() error = %v", err)
			}
			if got.IssueID != tt.wantID {
				t.Errorf("IssueID = %q, want %q", got.IssueID, tt.wantID)
			}
			if got.Site != tt.wantSite {
				t.Errorf("Site = %q, want %q", got.Site, tt.wantSite)
			}
		})
	}
}

// TestParseTargetErrors checks that each way of failing says what to do about
// it. The messages matter more here than anywhere else in the tool: this is the
// only code a user's own input reaches, and a generic parse error sends someone
// hunting rather than pointing at the fix.
func TestParseTargetErrors(t *testing.T) {
	tests := map[string]struct {
		raw      string
		wantInfo string // a phrase the message must carry
	}{
		"empty input": {raw: "  ", wantInfo: "paste"},

		// The likeliest bad paste: the explorer, with no issue opened.
		"explorer with nothing selected": {
			raw:      "https://app.datadoghq.com/error-tracking?query=&live=true",
			wantInfo: "no issue selected",
		},
		"explorer with some other panel": {
			raw:      "https://app.datadoghq.com/error-tracking?sp=%5B%7B%22p%22%3A%7B%22x%22%3A1%7D%2C%22i%22%3A%22logs%22%7D%5D",
			wantInfo: "not an issue one",
		},

		// sp is undocumented UI state. A shape change has to surface here,
		// rather than as an empty id that fails three steps later.
		"sp is not an array of panels": {
			raw:      "https://app.datadoghq.com/error-tracking?sp=%7B%22i%22%3A%22error-tracking-issue%22%7D",
			wantInfo: "changed its format",
		},
		"id in the path is not a uuid": {
			raw:      "https://app.datadoghq.com/error-tracking/issue/not-a-uuid",
			wantInfo: "not an issue id",
		},

		// Suffix matching with a leading dot. Contains("datadoghq.com") would
		// accept this and send credentials to someone else's server.
		"a lookalike host is not datadog": {
			raw:      "https://app.datadoghq.com.evil.test/error-tracking/issue/" + issueID,
			wantInfo: "not a Datadog host",
		},

		// A monitor notification link is a different Datadog page with no
		// issue id in it. Rejected for now; the message says which page it is.
		"monitor notification link": {
			raw:      "https://app.datadoghq.com/monitors/318477005?event_id=x&link_source=monitor_notif",
			wantInfo: "/monitors/318477005",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ParseTarget(tt.raw)
			if err == nil {
				t.Fatalf("ParseTarget() error = nil, want one; got %+v", got)
			}
			if !strings.Contains(err.Error(), tt.wantInfo) {
				t.Errorf("error does not mention %q:\n  %v", tt.wantInfo, err)
			}
			// Nothing half-resolved escapes on the error path, so a caller
			// cannot act on a Target it was told was invalid.
			if got != (Target{}) {
				t.Errorf("Target = %+v, want the zero value on error", got)
			}
		})
	}
}
