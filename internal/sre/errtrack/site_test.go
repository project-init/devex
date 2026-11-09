package errtrack

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestDatadogBaseURL pins the precedence. A set of keys works only on its own
// site, so sending them to the wrong host fails with a 403 that reads exactly
// like a missing permission — the least obvious failure in the tool.
func TestDatadogBaseURL(t *testing.T) {
	tests := map[string]struct {
		flagSite, linkSite string
		env                map[string]string
		want               string
	}{
		"nothing set falls back to US1": {want: "https://api." + defaultSite},
		"flag wins over everything": {
			flagSite: "datadoghq.eu", linkSite: "us3.datadoghq.com",
			env:  map[string]string{siteEnv: "us5.datadoghq.com"},
			want: "https://api.datadoghq.eu",
		},
		// The link names the org whose issue is being fetched, so it beats a
		// variable exported some time ago.
		"link beats the environment": {
			linkSite: "datadoghq.eu",
			env:      map[string]string{siteEnv: defaultSite},
			want:     "https://api.datadoghq.eu",
		},
		"DD_SITE when there is no link": {
			env:  map[string]string{siteEnv: "datadoghq.eu"},
			want: "https://api.datadoghq.eu",
		},
		"DATADOG_SITE is the older spelling": {
			env:  map[string]string{datadogSiteEnv: "datadoghq.eu"},
			want: "https://api.datadoghq.eu",
		},
		// cmp.Or treats "" as zero and moves on, which is what an exported but
		// empty variable should do. Pinned so nobody turns this into a lookup
		// that returns empty and builds https://api.
		"exported but empty DD_SITE is skipped": {
			env:  map[string]string{siteEnv: "", datadogSiteEnv: "datadoghq.eu"},
			want: "https://api.datadoghq.eu",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := datadogBaseURL(tt.flagSite, tt.linkSite, env(tt.env))
			if got != tt.want {
				t.Errorf("datadogBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}

	// ParseTarget strips a leading app. from the link host. If that regresses
	// the result is https://api.app.datadoghq.com, which fails in a way that
	// points nowhere near either function.
	target, err := ParseTarget(issuePageLink)
	if err != nil {
		t.Fatalf("ParseTarget() error = %v", err)
	}
	if got := datadogBaseURL("", target.Site, env(nil)); got != "https://api.datadoghq.com" {
		t.Errorf("datadogBaseURL() = %q, want https://api.datadoghq.com", got)
	}
}

// TestSiteConflict covers when the note is worth printing. The keys most likely
// belong to the org the environment names, so following the link can 403 while
// following the environment fetches the wrong org — neither is right, and a
// silent pick turns into an inexplicable failure.
func TestSiteConflict(t *testing.T) {
	tests := map[string]struct {
		flagSite, linkSite string
		env                map[string]string
		wantNote           bool
	}{
		"link and environment disagree": {
			linkSite: "datadoghq.eu", env: map[string]string{siteEnv: defaultSite}, wantNote: true,
		},
		"they agree": {
			linkSite: "datadoghq.eu", env: map[string]string{siteEnv: "datadoghq.eu"},
		},
		"no environment to disagree with": {linkSite: "datadoghq.eu"},
		"no link, so nothing to compare":  {env: map[string]string{siteEnv: "datadoghq.eu"}},
		// An explicit flag settles it, so there is nothing to warn about.
		"the flag settles the question": {
			flagSite: "us3.datadoghq.com", linkSite: "datadoghq.eu",
			env: map[string]string{siteEnv: defaultSite},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := siteConflict(tt.flagSite, tt.linkSite, env(tt.env))

			if !tt.wantNote {
				if got != "" {
					t.Errorf("siteConflict() = %q, want no note", got)
				}

				return
			}
			if got == "" {
				t.Fatal("siteConflict() = \"\", want a note")
			}
			// The note has to name the site that won and how to override it,
			// or it explains nothing.
			for _, want := range []string{tt.linkSite, "--datadog-site"} {
				if !strings.Contains(got, want) {
					t.Errorf("note does not mention %q:\n  %s", want, got)
				}
			}
		})
	}
}
