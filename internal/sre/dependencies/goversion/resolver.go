package goversion

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
)

const (
	// releasesURL lists every Go release, including older minor lines that the default go.dev
	// feed omits once they leave support. It runs to megabytes.
	releasesURL = "https://go.dev/dl/?mode=json&include=all"
	// supportedURL lists the newest release of each supported minor line, in kilobytes.
	supportedURL = "https://go.dev/dl/?mode=json"
)

// Resolver fetches the published Go releases from go.dev.
type Resolver struct {
	URL          string // overrides releasesURL
	SupportedURL string // overrides supportedURL
	Client       *http.Client
}

type release struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// Releases returns every stable release, newest first.
func (r Resolver) Releases(ctx context.Context) ([]Version, error) {
	return r.fetch(ctx, cmp.Or(r.URL, releasesURL))
}

// Supported returns the newest stable release of each supported minor line, newest first.
func (r Resolver) Supported(ctx context.Context) ([]Version, error) {
	return r.fetch(ctx, cmp.Or(r.SupportedURL, supportedURL))
}

func (r Resolver) fetch(ctx context.Context, url string) ([]Version, error) {
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Go releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Go releases: %s returned %s", url, resp.Status)
	}

	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode Go releases: %w", err)
	}

	var versions []Version
	for _, rel := range releases {
		if !rel.Stable {
			continue
		}
		v, err := Parse(rel.Version)
		if err != nil {
			continue
		}
		// Before Go 1.21, a minor line's first release is named "go1.20", not "go1.20.0".
		v.Precision = 3
		versions = append(versions, v)
	}
	slices.SortFunc(versions, func(a, b Version) int { return b.Compare(a) })

	return versions, nil
}

// LatestPatch returns the newest release on current's minor line, never older than current.
func LatestPatch(releases []Version, current Version) (Version, error) {
	for _, v := range releases {
		if v.SameMinor(current) {
			return maxVersion(v, current), nil
		}
	}

	return Version{}, fmt.Errorf("go.dev lists no stable release on the %d.%d line", current.Major, current.Minor)
}

// Latest returns the newest release, never older than current.
func Latest(releases []Version, current Version) (Version, error) {
	if len(releases) == 0 {
		return Version{}, fmt.Errorf("go.dev lists no stable releases")
	}

	return maxVersion(releases[0], current), nil
}

// Released reports whether v, at full precision, appears in releases.
func Released(releases []Version, v Version) bool {
	return v.Precision == 3 && slices.Contains(releases, v)
}

func maxVersion(release, current Version) Version {
	if current.Precision == 3 && current.Compare(release) > 0 {
		return current
	}

	return release
}
