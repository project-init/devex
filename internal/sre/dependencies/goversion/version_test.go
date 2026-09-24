package goversion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}

	return v
}

func TestParseRoundTripsPrecision(t *testing.T) {
	cases := map[string]string{
		"1.26":     "1.26",
		"1.26.6":   "1.26.6",
		"go1.26.6": "1.26.6",
		" 1.27.1 ": "1.27.1",
	}
	for in, want := range cases {
		if got := mustParse(t, in).String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
}

func TestParseRejectsNonReleases(t *testing.T) {
	for _, in := range []string{"1", "1.27rc1", "latest", "1.26.x", "", "v1.26.6", "01.26", "1.026", "1.26.06"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}

func TestFullAlwaysHasThreeComponents(t *testing.T) {
	if got := mustParse(t, "1.26").Full(); got != "1.26.0" {
		t.Errorf("Full() = %q, want 1.26.0", got)
	}
}

func TestAgrees(t *testing.T) {
	cases := []struct {
		pin, current string
		want         bool
	}{
		{"1.26", "1.26.6", true},
		{"1.26.6", "1.26.6", true},
		{"1.26.5", "1.26.6", false},
		{"1.27", "1.26.6", false},
		{"1.26.6", "1.26", true},
	}
	for _, c := range cases {
		if got := mustParse(t, c.pin).Agrees(mustParse(t, c.current)); got != c.want {
			t.Errorf("%s.Agrees(%s) = %v, want %v", c.pin, c.current, got, c.want)
		}
	}
}

func TestAtMost(t *testing.T) {
	cases := []struct {
		floor, toolchain string
		want             bool
	}{
		{"1.26.6", "1.26.6", true},
		{"1.26.0", "1.27.1", true},
		{"1.27.0", "1.26.6", false},
		{"1.26.6", "1.26", true},
		{"1.27.0", "1.26", false},
	}
	for _, c := range cases {
		if got := mustParse(t, c.floor).AtMost(mustParse(t, c.toolchain)); got != c.want {
			t.Errorf("%s.AtMost(%s) = %v, want %v", c.floor, c.toolchain, got, c.want)
		}
	}
}

func TestPrecedes(t *testing.T) {
	cases := []struct {
		v, o string
		want bool
	}{
		{"1.22", "1.22.0", true},
		{"1.22.0", "1.22.1", true},
		{"1.22.0", "1.22", false},
		{"1.22.1", "1.22.1", false},
		{"1.23", "1.22.9", false},
	}
	for _, c := range cases {
		if got := mustParse(t, c.v).Precedes(mustParse(t, c.o)); got != c.want {
			t.Errorf("%s.Precedes(%s) = %v, want %v", c.v, c.o, got, c.want)
		}
	}
}

func TestMinorFloor(t *testing.T) {
	if got := mustParse(t, "1.27.1").MinorFloor().String(); got != "1.27.0" {
		t.Errorf("MinorFloor() = %q, want 1.27.0", got)
	}
}

const releasesFixture = `[
  {"version": "go1.28rc1", "stable": false},
  {"version": "go1.27.1", "stable": true},
  {"version": "go1.27.0", "stable": true},
  {"version": "go1.26.9", "stable": true},
  {"version": "go1.26.6", "stable": true},
  {"version": "go1.20", "stable": true}
]`

func fixtureReleases(t *testing.T) []Version {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(releasesFixture))
	}))
	t.Cleanup(srv.Close)

	releases, err := Resolver{URL: srv.URL, Client: srv.Client()}.Releases(context.Background())
	if err != nil {
		t.Fatalf("Releases: %v", err)
	}

	return releases
}

func TestReleasesExcludesPrereleasesAndSortsNewestFirst(t *testing.T) {
	releases := fixtureReleases(t)
	if len(releases) != 5 {
		t.Fatalf("got %d releases, want 5 stable", len(releases))
	}
	if releases[0].String() != "1.27.1" {
		t.Errorf("newest = %s, want 1.27.1", releases[0])
	}
	if last := releases[len(releases)-1]; last.String() != "1.20.0" {
		t.Errorf("go1.20 normalized to %s, want 1.20.0", last)
	}
}

func TestReleasesFailsOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := (Resolver{URL: srv.URL, Client: srv.Client()}).Releases(context.Background()); err == nil {
		t.Fatal("Releases succeeded on a 502, want error")
	}
}

func TestLatestPatchStaysOnMinorLine(t *testing.T) {
	got, err := LatestPatch(fixtureReleases(t), mustParse(t, "1.26.6"))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1.26.9" {
		t.Errorf("LatestPatch = %s, want 1.26.9", got)
	}
}

func TestLatestPatchFromTwoComponentPin(t *testing.T) {
	got, err := LatestPatch(fixtureReleases(t), mustParse(t, "1.26"))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1.26.9" {
		t.Errorf("LatestPatch = %s, want 1.26.9", got)
	}
}

func TestLatestPatchFailsOnUnknownLine(t *testing.T) {
	if _, err := LatestPatch(fixtureReleases(t), mustParse(t, "1.24.2")); err == nil {
		t.Fatal("LatestPatch succeeded for an unlisted minor line, want error")
	}
}

func TestLatestCrossesMinors(t *testing.T) {
	got, err := Latest(fixtureReleases(t), mustParse(t, "1.26.6"))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1.27.1" {
		t.Errorf("Latest = %s, want 1.27.1", got)
	}
}

func TestTargetsNeverDowngrade(t *testing.T) {
	current := mustParse(t, "1.27.3")
	got, err := Latest(fixtureReleases(t), current)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1.27.3" {
		t.Errorf("Latest = %s, want current 1.27.3 kept", got)
	}
}

func TestReleased(t *testing.T) {
	releases := fixtureReleases(t)
	if !Released(releases, mustParse(t, "1.26.9")) {
		t.Error("1.26.9 reported unreleased")
	}
	if Released(releases, mustParse(t, "1.26.7")) {
		t.Error("1.26.7 reported released")
	}
	if Released(releases, mustParse(t, "1.26")) {
		t.Error("a two-component version must not count as a release")
	}
}
