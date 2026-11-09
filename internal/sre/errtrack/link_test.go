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
// it. The messages are the whole point: this is the one place a user's own
// input reaches, and a generic parse error sends someone hunting.
func TestParseTargetErrors(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantInfo string // a phrase the message must carry
	}{
		{name: "empty input", raw: "", wantInfo: "paste"},
		{name: "only whitespace", raw: "   ", wantInfo: "paste"},

		// The likeliest bad paste: the explorer with no issue opened.
		{
			name: "explorer with nothing selected", wantInfo: "no issue selected",
			raw: "https://app.datadoghq.com/error-tracking?query=&live=true",
		},
		{
			name: "explorer with some other panel", wantInfo: "not an issue one",
			raw: "https://app.datadoghq.com/error-tracking?sp=%5B%7B%22p%22%3A%7B%22x%22%3A1%7D%2C%22i%22%3A%22logs%22%7D%5D",
		},

		// sp is undocumented UI state. A shape change has to surface here, not
		// as an empty id that fails three steps later.
		{
			name: "sp is not json", wantInfo: "changed its format",
			raw: "https://app.datadoghq.com/error-tracking?sp=not-json",
		},
		{
			name: "sp is json but not an array", wantInfo: "changed its format",
			raw: "https://app.datadoghq.com/error-tracking?sp=%7B%22i%22%3A%22error-tracking-issue%22%7D",
		},
		{
			name: "issue panel carries no id", wantInfo: "not an issue id",
			raw: "https://app.datadoghq.com/error-tracking?sp=%5B%7B%22p%22%3A%7B%7D%2C%22i%22%3A%22error-tracking-issue%22%7D%5D",
		},

		{
			name: "id in the path is not a uuid", wantInfo: "not an issue id",
			raw: "https://app.datadoghq.com/error-tracking/issue/not-a-uuid",
		},
		{
			name: "not a datadog host", wantInfo: "not a Datadog host",
			raw: "https://example.com/error-tracking/issue/" + issueID,
		},
		{
			name: "a lookalike host is not datadog", wantInfo: "not a Datadog host",
			raw: "https://app.datadoghq.com.evil.test/error-tracking/issue/" + issueID,
		},

		// A monitor notification link is a different Datadog page with no issue
		// id in it at all. Rejected for now; the message says which page it is.
		{
			name: "monitor notification link", wantInfo: "/monitors/318477005",
			raw: "https://app.datadoghq.com/monitors/318477005?event_id=x&link_source=monitor_notif",
		},
		{
			name: "some other datadog page", wantInfo: "Error Tracking link",
			raw: "https://app.datadoghq.com/logs?query=foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
