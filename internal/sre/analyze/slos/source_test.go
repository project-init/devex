package slos

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/sre/config"
)

func readTestdata(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/slos.gen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestParseSLOs reads every RPC of the group in file order, with its tags, and reads a null slo as none. An RPC with
// its own evaluation window is rolled up to that instead of --rollup.
func TestParseSLOs(t *testing.T) {
	eps, err := parseSLOs(readTestdata(t), "orders", "env:prod", 15*time.Minute)
	if err != nil {
		t.Fatalf("parseSLOs: %v", err)
	}
	type got struct {
		name, metric string
		slo          float64
		tags         string
		rollup       time.Duration
	}
	var gots []got
	for _, ep := range eps {
		gots = append(gots, got{ep.Name(), ep.Metric, ep.SLO, strings.Join(ep.Tags, ","), ep.Rollup})
	}
	want := []got{
		{"OrdersService.GetOrder", "acmeServicesOrdersV1OrdersServiceGetOrder", 500, "p0,apps-primary", 15 * time.Minute},
		{"OrdersService.ListOrders", "acmeServicesOrdersV1OrdersServiceListOrders", 0, "", time.Hour},
	}
	if !slices.Equal(gots, want) {
		t.Errorf("endpoints\n got %+v\nwant %+v", gots, want)
	}
}

// TestParseSLOsGroupErrors names the groups to choose from, and tells an empty group apart from an unknown one.
func TestParseSLOsGroupErrors(t *testing.T) {
	cases := map[string]struct{ group, want string }{
		"no group":      {group: "", want: "pass --group"},
		"unknown group": {group: "missing", want: `no group "missing"`},
		"empty group":   {group: "shipping", want: "has no RPCs"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSLOs(readTestdata(t), tc.group, "*", 15*time.Minute)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one that says %q", err, tc.want)
			}
			if tc.group == "shipping" {
				return
			}
			for _, group := range []string{"orders", "shipping", "inventory"} {
				if !strings.Contains(err.Error(), group) {
					t.Errorf("error = %v, want one that names %s", err, group)
				}
			}
		})
	}
}

// TestFilterByTags keeps the RPCs carrying any of the tags, and every RPC when there are none.
func TestFilterByTags(t *testing.T) {
	eps, err := parseSLOs(readTestdata(t), "orders", "*", 15*time.Minute)
	if err != nil {
		t.Fatalf("parseSLOs: %v", err)
	}
	cases := map[string][]string{
		"":             {"OrdersService.GetOrder", "OrdersService.ListOrders"},
		"p0":           {"OrdersService.GetOrder"},
		"apps-primary": {"OrdersService.GetOrder"},
		"p4":           nil,
	}
	for tag, want := range cases {
		t.Run(tag, func(t *testing.T) {
			var tags []string
			if tag != "" {
				tags = []string{tag}
			}
			var got []string
			for _, ep := range filterByTags(eps, tags) {
				got = append(got, ep.Name())
			}
			if !slices.Equal(got, want) {
				t.Errorf("filterByTags(%q) = %v, want %v", tag, got, want)
			}
		})
	}
}

// TestParseSLOsVersion refuses any version but 1 before it reads the rest, so a new shape fails on its version.
func TestParseSLOsVersion(t *testing.T) {
	cases := []struct {
		name, data, want string
	}{
		{name: "a newer version", data: "version: 2\nservices: [a, b]\n", want: "version 2"},
		{name: "no version", data: "services: {}\n", want: "version 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSLOs([]byte(tc.data), "orders", "*", 15*time.Minute)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "version 1") {
				t.Errorf("error = %v, want one that names %q and the version 1 devex reads", err, tc.want)
			}
		})
	}
}

const testModule = "github.com/acme/protos"

func TestResolveModule(t *testing.T) {
	cases := []struct {
		name, goMod, fallback string
		want                  moduleRef
		wantErr               string
	}{
		{
			name:  "the required version",
			goMod: "require github.com/acme/protos v1.2.3\n",
			want:  moduleRef{Path: testModule, Version: "v1.2.3"},
		},
		{
			name:  "a pseudo-version, as a branch pin gives",
			goMod: "require github.com/acme/protos v1.2.4-0.20260101000000-abcdef123456\n",
			want:  moduleRef{Path: testModule, Version: "v1.2.4-0.20260101000000-abcdef123456"},
		},
		{
			name:  "a replace to a local path, relative to go.mod",
			goMod: "require github.com/acme/protos v1.2.3\nreplace github.com/acme/protos => ../protos\n",
			want:  moduleRef{Dir: "../protos"},
		},
		{
			name:  "a replace to another module version",
			goMod: "require github.com/acme/protos v1.2.3\nreplace github.com/acme/protos => github.com/fork/protos v1.2.3-fix\n",
			want:  moduleRef{Path: "github.com/fork/protos", Version: "v1.2.3-fix"},
		},
		{
			name:  "a replace of another version does not apply",
			goMod: "require github.com/acme/protos v1.2.3\nreplace github.com/acme/protos v1.0.0 => ../protos\n",
			want:  moduleRef{Path: testModule, Version: "v1.2.3"},
		},
		{
			name:     "slos.version when go.mod does not require the module",
			goMod:    "require github.com/other/mod v1.0.0\n",
			fallback: "v1.5.0",
			want:     moduleRef{Path: testModule, Version: "v1.5.0"},
		},
		{
			name:    "no require and no slos.version",
			goMod:   "require github.com/other/mod v1.0.0\n",
			wantErr: "slos.version",
		},
		{
			name:     "slos.version with no go.mod",
			fallback: "v1.5.0",
			want:     moduleRef{Path: testModule, Version: "v1.5.0"},
		},
		{
			name:    "no go.mod and no slos.version",
			wantErr: "slos.version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			goModPath := filepath.Join(dir, "go.mod")
			if tc.goMod != "" {
				if err := os.WriteFile(goModPath, []byte("module example.com/app\n\ngo 1.27\n\n"+tc.goMod), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want := tc.want
			if want.Dir != "" {
				want.Dir = filepath.Join(dir, want.Dir)
			}
			got, err := resolveModule(goModPath, testModule, tc.fallback)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want one that says %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.String() != want.String() {
				t.Errorf("resolveModule = %s, %v; want %s", got, err, want)
			}
		})
	}
}

// fakeModules is a downloader over module directories made in a temp dir, keyed by version. latest names the
// version "latest" resolves to.
type fakeModules struct {
	root   string
	latest string
	asked  []string
}

func (f *fakeModules) add(t *testing.T, version string, withFile bool) {
	t.Helper()
	dir := filepath.Join(f.root, version)
	if err := os.MkdirAll(filepath.Join(dir, "sre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withFile {
		if err := os.WriteFile(filepath.Join(dir, "sre", "slos.gen.yaml"), []byte("version: 1\n# "+version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fakeModules) download(_ context.Context, path, version string) (string, string, error) {
	f.asked = append(f.asked, path+"@"+version)
	if version == "latest" {
		version = f.latest
	}
	return filepath.Join(f.root, version), version, nil
}

// TestReadSLOFile reads the file from the pinned release, and names the release to bump to when the pinned one has
// none.
func TestReadSLOFile(t *testing.T) {
	cfg := config.SLOsConfiguration{Module: testModule, File: "sre/slos.gen.yaml"}
	cases := []struct {
		name, pinned  string
		pinnedHasFile bool
		latestHasFile bool
		want, wantErr string
	}{
		{name: "the pinned release has the file", pinned: "v1.3.0", pinnedHasFile: true, latestHasFile: true, want: "# v1.3.0"},
		{name: "a newer release has the file", pinned: "v1.1.0", latestHasFile: true, wantErr: "go get github.com/acme/protos@v1.4.0"},
		{name: "no release has the file", pinned: "v1.1.0", wantErr: "no release"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mods := &fakeModules{root: filepath.Join(dir, "mods"), latest: "v1.4.0"}
			mods.add(t, tc.pinned, tc.pinnedHasFile)
			mods.add(t, mods.latest, tc.latestHasFile)
			goModPath := filepath.Join(dir, "go.mod")
			if err := os.WriteFile(goModPath, []byte("module example.com/app\n\nrequire github.com/acme/protos "+tc.pinned+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			data, _, err := readSLOFile(context.Background(), cfg, goModPath, "", mods.download)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want one that says %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !strings.Contains(string(data), tc.want) {
				t.Errorf("readSLOFile = %q, %v; want the file from %s", data, err, tc.pinned)
			}
		})
	}
}

// TestReadSLOFileLocalReplace reads the file from the replaced directory and downloads nothing.
func TestReadSLOFileLocalReplace(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "protos", "sre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protos", "sre", "slos.gen.yaml"), []byte("version: 1\n# local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module example.com/app\n\nrequire github.com/acme/protos v1.0.0\nreplace github.com/acme/protos => ./protos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mods := &fakeModules{root: dir}
	cfg := config.SLOsConfiguration{Module: testModule, File: "sre/slos.gen.yaml"}
	data, _, err := readSLOFile(context.Background(), cfg, goModPath, "", mods.download)
	if err != nil || !strings.Contains(string(data), "# local") || len(mods.asked) != 0 {
		t.Errorf("readSLOFile = %q, %v, downloads %v; want the local file and no download", data, err, mods.asked)
	}

	// A local checkout that has not generated the file yet is not sent to look for a release.
	cfg.File = "sre/missing.yaml"
	if _, _, err := readSLOFile(context.Background(), cfg, goModPath, "", mods.download); err == nil ||
		!strings.Contains(err.Error(), "sre/missing.yaml") || len(mods.asked) != 0 {
		t.Errorf("a local replace without the file: error = %v, downloads %v; want one that names the file, and no download", err, mods.asked)
	}
}

// TestFindSLOModule names the one module go.mod requires that ships the SLO file at the required version, through a
// replace, and asks for --module when there is none or more than one.
func TestFindSLOModule(t *testing.T) {
	const file = "sre/slos.gen.yaml"
	cases := []struct {
		name, goMod   string
		want, wantErr string
	}{
		{
			name:  "the one module that ships the file",
			goMod: "require (\n\tgithub.com/other/mod v1.0.0\n\tgithub.com/acme/protos v1.2.0\n)\n",
			want:  testModule,
		},
		{
			name:    "the required version does not ship it",
			goMod:   "require github.com/acme/protos v1.1.0\n",
			wantErr: "bump the module",
		},
		{
			name:  "a replace to a local path",
			goMod: "require github.com/acme/protos v1.1.0\nreplace github.com/acme/protos => ./protos\n",
			want:  testModule,
		},
		{
			name:  "a replace to a fork names the required module",
			goMod: "require github.com/acme/protos v1.1.0\nreplace github.com/acme/protos => github.com/fork/protos v1.2.0-fix\n",
			want:  testModule,
		},
		{
			name:    "more than one module ships it",
			goMod:   "require (\n\tgithub.com/acme/protos v1.2.0\n\tgithub.com/fork/protos v1.2.0-fix\n)\n",
			wantErr: "github.com/acme/protos, github.com/fork/protos each ship",
		},
		{
			name:    "no go.mod",
			wantErr: "pass --module",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cache := filepath.Join(dir, "cache")
			for _, d := range []string{
				filepath.Join(cache, "github.com/acme/protos@v1.2.0"),
				filepath.Join(cache, "github.com/fork/protos@v1.2.0-fix"),
				filepath.Join(dir, "protos"),
			} {
				if err := os.MkdirAll(filepath.Join(d, "sre"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(d, file), []byte("version: 1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// Downloaded, but from before the release that added the file.
			if err := os.MkdirAll(filepath.Join(cache, "github.com/acme/protos@v1.1.0"), 0o755); err != nil {
				t.Fatal(err)
			}
			goModPath := filepath.Join(dir, "go.mod")
			if tc.goMod != "" {
				if err := os.WriteFile(goModPath, []byte("module example.com/app\n\ngo 1.27\n\n"+tc.goMod), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := findSLOModule(context.Background(), goModPath, file, func(context.Context) (string, error) { return cache, nil })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want one that says %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("findSLOModule = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
