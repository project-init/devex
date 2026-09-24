package gosync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"github.com/project-init/devex/internal/sre/dependencies/registry"
)

type fakeResolver []string

func (f fakeResolver) Releases(context.Context) ([]goversion.Version, error) {
	var out []goversion.Version
	for _, s := range f {
		v, _ := goversion.Parse(s)
		out = append(out, v)
	}

	return out, nil
}

var releases = fakeResolver{"1.27.1", "1.27.0", "1.26.9", "1.26.6"}

// fakeRegistry publishes the tags it lists; any other tag returns err.
type fakeRegistry struct {
	digests map[string]string
	err     error
}

func (f *fakeRegistry) Digest(_ context.Context, image, tag string) (string, error) {
	if d, ok := f.digests[image+":"+tag]; ok {
		return d, nil
	}
	if f.err != nil {
		return "", f.err
	}

	return "", registry.ErrNotFound
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	root := t.TempDir()
	if err := os.CopyFS(root, fsys); err != nil {
		t.Fatal(err)
	}

	return root
}

func settings(t *testing.T, root string, cfg config.GoDependenciesConfiguration) Settings {
	t.Helper()
	s, err := NewSettings(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Discovery.ListFiles = pins.WalkFiles

	return s
}

func changesByLocation(plan Plan) map[string]string {
	out := map[string]string{}
	for _, c := range plan.Changes {
		out[c.File+":"+string(c.Kind)] = c.From + "→" + c.To
	}

	return out
}

const appTree = "module app\n\ngo 1.26.6\n"

func TestNewSettingsDefaultsAndValidation(t *testing.T) {
	s, err := NewSettings(".", config.GoDependenciesConfiguration{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Target != TargetPatch || s.Directive != DirectiveNone {
		t.Errorf("defaults = %s/%s, want patch/none", s.Target, s.Directive)
	}

	for _, cfg := range []config.GoDependenciesConfiguration{
		{Target: "minor"},
		{Directive: "floor"},
		{Pins: []config.GoPinConfiguration{{Files: []string{"x"}, Pattern: `(\d+)`}}},
	} {
		if _, err := NewSettings(".", cfg); err == nil {
			t.Errorf("NewSettings(%+v) succeeded, want error", cfg)
		}
	}
}

func TestCurrentAcceptsLowerPrecisionPins(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"mise.toml":  "[tools]\ngo = \"1.26.6\"\n",
		"Dockerfile": "FROM golang:1.26-alpine\n",
	})
	found, _ := pins.Discover(settings(t, root, config.GoDependenciesConfiguration{}).Discovery)
	current, err := Current(found.Pins)
	if err != nil {
		t.Fatal(err)
	}
	if current.String() != "1.26.6" {
		t.Errorf("Current = %s, want 1.26.6", current)
	}
}

func TestCheckReportsBusinessPlatformDrift(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":                appTree,
		"mise.toml":             "[tools]\ngo = \"1.27.1\"\n",
		"docker/api/Dockerfile": "ARG GO_IMAGE_TAG=1.26.6-alpine3.24\nFROM golang:${GO_IMAGE_TAG}\n",
	})
	found, _ := pins.Discover(settings(t, root, config.GoDependenciesConfiguration{}).Discovery)
	_, err := Check(found, DirectiveNone)
	var perr *ProblemsError
	if !errors.As(err, &perr) || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("err = %v, want a drift report", err)
	}
	for _, want := range []string{"mise.toml:2", "1.27.1", "docker/api/Dockerfile:1", "1.26.6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("report %q lacks %q", err, want)
		}
	}
}

func TestCheckReportsFloorAboveDockerToolchain(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     "module app\n\ngo 1.27.0\n",
		"Dockerfile": "FROM golang:1.26.6-alpine3.24\n",
	})
	found, _ := pins.Discover(settings(t, root, config.GoDependenciesConfiguration{}).Discovery)
	_, err := Check(found, DirectiveNone)
	if err == nil || !strings.Contains(err.Error(), "above toolchain 1.26.6") {
		t.Fatalf("err = %v, want the go.mod floor flagged", err)
	}
}

func TestCheckDirectivePolicies(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":    "module lib\n\ngo 1.26.0\n",
		"mise.toml": "[tools]\ngo = \"1.27.1\"\n",
	})
	found, _ := pins.Discover(settings(t, root, config.GoDependenciesConfiguration{}).Discovery)
	if _, err := Check(found, DirectiveNone); err != nil {
		t.Errorf("none: %v, want protos-style floor accepted", err)
	}
	if _, err := Check(found, DirectiveMinor); err == nil {
		t.Error("minor accepted go 1.26.0 under toolchain 1.27.1")
	}
	if _, err := Check(found, DirectiveExact); err == nil {
		t.Error("exact accepted go 1.26.0 under toolchain 1.27.1")
	}
}

func TestGoWorkFloorFollowsItsModules(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.work":   "go 1.26\n\nuse ./a\n",
		"a/go.mod":  "module a\n\ngo 1.26.0\n",
		"b/go.mod":  "module b\n\ngo 1.26.5\n",
		"mise.toml": "[tools]\ngo = \"1.26.6\"\n",
	})
	found, err := pins.Discover(settings(t, root, config.GoDependenciesConfiguration{}).Discovery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(found, DirectiveNone); err == nil || !strings.Contains(err.Error(), "below a/go.mod's go 1.26.0") {
		t.Errorf("err = %v, want the go.work floor flagged below its module", err)
	}
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{}}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Change{File: "go.work", Line: 1, Kind: pins.KindWorkGo, Span: pins.Span{Start: 3, End: 7}, From: "1.26", To: "1.26.0"}
	if !slices.Contains(plan.Changes, want) {
		t.Errorf("changes = %+v, want the go.work floor raised to its module's", plan.Changes)
	}
}

func TestPlanPatchTargetSyncsEveryPin(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":                    appTree,
		"mise.toml":                 "[tools]\ngo = \"1.26.6\"\n",
		"docker/api/Dockerfile":     "ARG GO_IMAGE_TAG=1.26.6-alpine3.24\nFROM golang:${GO_IMAGE_TAG}\n",
		".go-version":               "1.26.6\n",
		".github/workflows/ci.yaml": "jobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.26'\n",
	})
	reg := &fakeRegistry{digests: map[string]string{"golang:1.26.9-alpine3.24": "sha256:new"}}
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{Directive: DirectiveExact}), Resolver: releases, Registry: reg}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target.String() != "1.26.9" {
		t.Fatalf("target = %s, want 1.26.9", plan.Target)
	}
	got := changesByLocation(plan)
	want := map[string]string{
		"go.mod:go.mod go":                 "1.26.6→1.26.9",
		"mise.toml:mise":                   "1.26.6→1.26.9",
		"docker/api/Dockerfile:dockerfile": "1.26.6→1.26.9",
		".go-version:.go-version":          "1.26.6→1.26.9",
	}
	if len(got) != len(want) {
		t.Errorf("changes = %v, want %v (setup-go's 1.26 is already on the line)", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("change %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestPlanLatestTargetAndDirectivePolicies(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":    "module lib\n\ngo 1.26.0\n\ntoolchain go1.26.6\n",
		"mise.toml": "[tools]\ngo = \"1.26.6\"\n",
	})
	for directive, wantGo := range map[string]string{DirectiveNone: "", DirectiveMinor: "1.26.0→1.27.0", DirectiveExact: "1.26.0→1.27.1"} {
		plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{Target: TargetLatest, Directive: directive}), Resolver: releases, Registry: &fakeRegistry{}}.Plan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got := changesByLocation(plan)
		if got["go.mod:go.mod go"] != wantGo {
			t.Errorf("%s: go directive change %q, want %q", directive, got["go.mod:go.mod go"], wantGo)
		}
		if got["go.mod:go.mod toolchain"] != "1.26.6→1.27.1" {
			t.Errorf("%s: toolchain change %q, want 1.26.6→1.27.1", directive, got["go.mod:go.mod toolchain"])
		}
	}
}

func TestPlanFailsOnDriftUnlessOverridden(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"mise.toml":  "[tools]\ngo = \"1.27.1\"\n",
		"Dockerfile": "FROM golang:1.26.6\n",
	})
	s := settings(t, root, config.GoDependenciesConfiguration{})
	reg := &fakeRegistry{digests: map[string]string{"golang:1.26.9": "sha256:x"}}
	if _, err := (Planner{Settings: s, Resolver: releases, Registry: reg}).Plan(context.Background()); err == nil || !strings.Contains(err.Error(), "--go-version") {
		t.Fatalf("err = %v, want drift pointing at --go-version", err)
	}

	override, _ := goversion.Parse("1.26.9")
	plan, err := Planner{Settings: s, Resolver: releases, Registry: reg, Override: &override}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := changesByLocation(plan)["mise.toml:mise"]; got != "1.27.1→1.26.9" {
		t.Errorf("mise change = %q, want the accidental 1.27.1 pulled back", got)
	}

	bogus, _ := goversion.Parse("1.26.7")
	if _, err := (Planner{Settings: s, Resolver: releases, Registry: reg, Override: &bogus}).Plan(context.Background()); err == nil {
		t.Error("an unpublished --go-version was accepted")
	}
}

func TestPlanNeverLowersAFloor(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":    "module app\n\ngo 1.27.0\n",
		"mise.toml": "[tools]\ngo = \"1.26.6\"\n",
	})
	_, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{Directive: DirectiveExact}), Resolver: releases, Registry: &fakeRegistry{}}.Plan(context.Background())
	if err == nil || !strings.Contains(err.Error(), "never lowers a floor") {
		t.Fatalf("err = %v, want the 1.27.0 floor refused", err)
	}
}

func TestPlanFailsOnUnpublishedTagBeforeWriting(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"mise.toml":  "[tools]\ngo = \"1.26.6\"\n",
		"Dockerfile": "FROM golang:1.26.6-alpine3.24\n",
	})
	before, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	_, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{}}.Plan(context.Background())
	if err == nil || !strings.Contains(err.Error(), "golang:1.26.9-alpine3.24 is not published") {
		t.Fatalf("err = %v, want the missing tag named", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if string(before) != string(after) {
		t.Error("planning wrote to the Dockerfile")
	}
}

func TestPlanDigestPins(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"Dockerfile": "FROM golang:1.26.6@sha256:old\n",
	})
	reg := &fakeRegistry{digests: map[string]string{"golang:1.26.9": "sha256:new"}}
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: reg}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var digest *Change
	for i, c := range plan.Changes {
		if c.From == "sha256:old" {
			digest = &plan.Changes[i]
		}
	}
	if digest == nil || digest.To != "sha256:new" {
		t.Fatalf("changes = %+v, want the digest moved to sha256:new", plan.Changes)
	}

	_, err = Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{err: registry.ErrAuthRequired}}.Plan(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot refresh its digest pin") {
		t.Errorf("err = %v, want a private digest pin refused", err)
	}
}

func TestPlanWarnsAboutStaleMiseLock(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":    appTree,
		"mise.toml": "[tools]\ngo = \"1.26.6\"\n",
		"mise.lock": "# lock\n",
	})
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{}}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "mise.lock pins exact tool versions") {
		t.Errorf("warnings = %q, want the stale lock noted", plan.Warnings)
	}
}

func TestPlanRefreshesDigestOnUnchangedTag(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"mise.toml":  "[tools]\ngo = \"1.26.6\"\n",
		"Dockerfile": "FROM golang:1.26@sha256:old\n",
	})
	reg := &fakeRegistry{digests: map[string]string{"golang:1.26": "sha256:new"}}
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: reg}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Changes, func(c Change) bool { return c.From == "sha256:old" && c.To == "sha256:new" }) {
		t.Errorf("changes = %+v, want the golang:1.26 digest refreshed", plan.Changes)
	}

	// The old digest still matches the unchanged tag, so a failed refresh only warns.
	plan, err = Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{err: registry.ErrAuthRequired}}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "left its digest as is") {
		t.Errorf("warnings = %q, want the digest left as is", plan.Warnings)
	}
}

func TestCheckReportsWorkspaceProblemsAlongsideDrift(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.work":    "go 1.25.0\n\nuse ./a\n",
		"a/go.mod":   "module a\n\ngo 1.26.0\n",
		"mise.toml":  "[tools]\ngo = \"1.26.6\"\n",
		"Dockerfile": "FROM golang:1.26.5\n",
	})
	found, _ := pins.Discover(settings(t, root, config.GoDependenciesConfiguration{}).Discovery)
	_, err := Check(found, DirectiveNone)
	for _, want := range []string{"drift", "below a/go.mod's go 1.26.0"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to report %q", err, want)
		}
	}
}

func TestPlanWarnsOnPrivateRegistryTag(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"Dockerfile": "FROM 123.dkr.ecr.us-east-1.amazonaws.com/golang:1.26.6\n",
	})
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{err: registry.ErrAuthRequired}}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "wrote it unverified") {
		t.Errorf("warnings = %q, want the unverified tag noted", plan.Warnings)
	}
}

func TestApplyWritesNothingWhenAnySpanIsStale(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/Dockerfile": "FROM golang:1.26.6\n",
		"go.mod":       "module app\n\ngo 1.26.7\n",
	})
	changes := []Change{
		{File: "a/Dockerfile", Line: 1, Kind: pins.KindDockerfile, Span: pins.Span{Start: 12, End: 18}, From: "1.26.6", To: "1.26.9"},
		{File: "go.mod", Line: 3, Kind: pins.KindGoDirective, Span: pins.Span{Start: 15, End: 21}, From: "1.26.6", To: "1.26.9"},
	}
	err := Apply(root, changes)
	if err == nil || !strings.Contains(err.Error(), "changed since planning") {
		t.Fatalf("err = %v, want the stale span refused", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a/Dockerfile")); string(got) != "FROM golang:1.26.6\n" {
		t.Errorf("a/Dockerfile = %q, want it untouched", got)
	}
}

func TestApplyWritesEveryChangeInAMixedFile(t *testing.T) {
	data := "[tools]\ngo = \"1.26.6\"\n# GO=1.26.6\n"
	root := writeTree(t, map[string]string{"mise.toml": data})
	miseStart := strings.Index(data, "1.26.6")
	declStart := strings.LastIndex(data, "1.26.6")
	changes := []Change{
		{File: "mise.toml", Line: 2, Kind: pins.KindMise, Span: pins.Span{Start: miseStart, End: miseStart + 6}, From: "1.26.6", To: "1.26.9"},
		{File: "mise.toml", Line: 3, Kind: pins.KindDeclared, Span: pins.Span{Start: declStart, End: declStart + 6}, From: "1.26.6", To: "1.26.9"},
	}
	if err := Apply(root, changes); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "mise.toml"))
	if want := "[tools]\ngo = \"1.26.9\"\n# GO=1.26.9\n"; string(got) != want {
		t.Errorf("mise.toml = %q, want %q", got, want)
	}
}

func TestApplyWritesGoWork(t *testing.T) {
	root := writeTree(t, map[string]string{"go.work": "go 1.26.0\n\ntoolchain go1.26.6\n"})
	changes := []Change{
		{File: "go.work", Line: 3, Kind: pins.KindWorkToolchain, Span: pins.Span{Start: 23, End: 29}, From: "1.26.6", To: "1.26.9"},
	}
	if err := Apply(root, changes); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "go.work"))
	if want := "go 1.26.0\n\ntoolchain go1.26.9\n"; string(got) != want {
		t.Errorf("go.work = %q, want %q", got, want)
	}
}

func TestPlanLeavesRegistryFromBuildArgUnverified(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     appTree,
		"Dockerfile": "FROM ${ECR}/golang:1.26.6\n",
	})
	plan, err := Planner{Settings: settings(t, root, config.GoDependenciesConfiguration{}), Resolver: releases, Registry: &fakeRegistry{}}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "takes its registry from a build arg") {
		t.Errorf("warnings = %q, want the unverifiable registry noted", plan.Warnings)
	}
}

// holdingRunner fails go get -u with a needs-newer-Go refusal until every blocked module is
// held back by an explicit module@version argument.
type holdingRunner struct {
	blocked        map[string]string
	buildFails     bool
	noHostPackages bool
	calls          []string
}

func (r *holdingRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "go" && args[0] == "test" && r.noHostPackages {
		return &CommandError{Command: "go test", Stderr: []byte("go: warning: \"./...\" matched no packages\nno packages to test\n"), Err: errors.New("exit status 1")}
	}
	if name == "go" && args[0] == "test" && r.buildFails {
		return &CommandError{Command: "go test", Stderr: []byte("cannot use *yaml.Node\n"), Err: errors.New("exit status 1")}
	}
	if name != "go" || args[0] != "get" {
		return nil
	}
	var stderr strings.Builder
	for module, line := range r.blocked {
		if !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, module+"@") }) {
			stderr.WriteString(line + "\n")
		}
	}
	if stderr.Len() == 0 {
		return nil
	}

	return &CommandError{Command: "go get", Stderr: []byte(stderr.String()), Err: errors.New("exit status 1")}
}

func TestUpdateModulesHoldsBackModulesNeedingNewerGo(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": `module app

go 1.26.6

require (
	example.com/a v1.2.0
	k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff // indirect
)
`})
	run := &holdingRunner{blocked: map[string]string{
		"k8s.io/kube-openapi": "go: k8s.io/kube-openapi@v0.0.0-20260911184034-7970a1e230da requires go >= 1.27.0 (running go 1.26.8; GOTOOLCHAIN=go1.26.8)",
	}}
	target, _ := goversion.Parse("1.26.8")

	held, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0].Module != "k8s.io/kube-openapi" || held[0].Version != "v0.0.0-20250318190949-c8a335a9a2ff" || held[0].NeedsGo != "1.27.0" {
		t.Fatalf("held = %+v", held)
	}
	want := []string{
		"go get -u ./...",
		"go get -u ./... k8s.io/kube-openapi@v0.0.0-20250318190949-c8a335a9a2ff",
		"go mod tidy",
		strings.Join(HoldCheck, " "),
	}
	if strings.Join(run.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(run.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestUpdateModulesFailsWhenHeldGraphDoesNotBuild(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": "module app\n\ngo 1.26.6\n\nrequire k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff\n"})
	run := &holdingRunner{buildFails: true, blocked: map[string]string{
		"k8s.io/kube-openapi": "go: k8s.io/kube-openapi@v0.0.0-20260911184034-7970a1e230da requires go >= 1.27.0 (running go 1.26.8; GOTOOLCHAIN=go1.26.8)",
	}}
	target, _ := goversion.Parse("1.26.8")

	_, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, run)
	if err == nil || !strings.Contains(err.Error(), "does not compile with k8s.io/kube-openapi held back; if the holds caused it, wait for a Go release that supports them (they need Go 1.27.0)") {
		t.Errorf("err = %v, want the broken partial upgrade refused with the Go it needs", err)
	}
}

func TestUpdateModulesSkipsBuildWhenNothingHeld(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": appTree})
	run := &holdingRunner{}
	target, _ := goversion.Parse("1.26.8")

	if _, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, run); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(run.calls, strings.Join(HoldCheck, " ")) {
		t.Errorf("calls = %q, want no build when nothing was held", run.calls)
	}
}

func TestUpdateModulesSyncsWorkspacesAfterEveryModule(t *testing.T) {
	root := writeTree(t, map[string]string{"go.work": "go 1.26.0\n\nuse ./a\n", "a/go.mod": appTree, "b/go.mod": appTree})
	run := &holdingRunner{}
	target, _ := goversion.Parse("1.26.8")

	if _, err := UpdateModules(context.Background(), root, []string{"a", "b"}, []string{"go.work"}, target, run); err != nil {
		t.Fatal(err)
	}
	if want := []string{"go get -u ./...", "go mod tidy", "go get -u ./...", "go mod tidy", "go work use"}; !slices.Equal(run.calls, want) {
		t.Errorf("calls = %q, want %q", run.calls, want)
	}
}

func TestUpdateModulesRefreshesVendor(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": appTree, "vendor/modules.txt": "# vendored\n"})
	run := &holdingRunner{}
	target, _ := goversion.Parse("1.26.8")

	if _, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, run); err != nil {
		t.Fatal(err)
	}
	if want := []string{"go get -u ./...", "go mod tidy", "go mod vendor"}; !slices.Equal(run.calls, want) {
		t.Errorf("calls = %q, want %q", run.calls, want)
	}

	// A vendor/ beside a go.work belongs to go work vendor, even when a module shares the directory.
	root = writeTree(t, map[string]string{"go.work": "go 1.26.0\n\nuse ./a\n", "go.mod": appTree, "vendor/modules.txt": "# vendored\n", "a/go.mod": appTree})
	run = &holdingRunner{}
	if _, err := UpdateModules(context.Background(), root, []string{"a", "."}, []string{"go.work"}, target, run); err != nil {
		t.Fatal(err)
	}
	if want := []string{"go get -u ./...", "go mod tidy", "go get -u ./...", "go mod tidy", "go work use", "go work vendor"}; !slices.Equal(run.calls, want) {
		t.Errorf("calls = %q, want %q", run.calls, want)
	}
}

func TestUpdateModulesPassesAHoldWithNoHostPackages(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": `module app

go 1.26.6

require k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff
`})
	run := &holdingRunner{noHostPackages: true, blocked: map[string]string{
		"k8s.io/kube-openapi": "go: k8s.io/kube-openapi@v0.0.0-20260911184034-7970a1e230da requires go >= 1.27.0",
	}}
	target, _ := goversion.Parse("1.26.8")

	// Every package carries another GOOS's build tag, so go test finds none to compile here.
	if _, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, run); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateModulesFailsWhenBlockedModuleIsNotRequired(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": appTree})
	run := &holdingRunner{blocked: map[string]string{
		"example.com/new": "go: example.com/new@v1.0.0 requires go >= 1.27.0 (running go 1.26.8; GOTOOLCHAIN=go1.26.8)",
	}}
	target, _ := goversion.Parse("1.26.8")

	_, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, run)
	if err == nil || !strings.Contains(err.Error(), "not yet in go.mod to hold back") {
		t.Errorf("err = %v, want the unholdable module named", err)
	}
}

func TestUpdateModulesPropagatesOtherFailures(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": appTree})
	target, _ := goversion.Parse("1.26.8")
	failing := runnerFunc(func(string, ...string) error {
		return &CommandError{Command: "go get", Stderr: []byte("go: network unreachable\n"), Err: errors.New("exit status 1")}
	})

	if _, err := UpdateModules(context.Background(), root, []string{"."}, nil, target, failing); err == nil {
		t.Error("an unrelated go get failure was swallowed")
	}
}

type runnerFunc func(name string, args ...string) error

func (f runnerFunc) Run(_ context.Context, _ string, _ []string, name string, args ...string) error {
	return f(name, args...)
}

func TestHighestNeedKeepsPrereleases(t *testing.T) {
	if got := highestNeed([]Held{{NeedsGo: "1.26.9"}, {NeedsGo: "1.27rc1"}}); got != "1.27rc1" {
		t.Errorf("highestNeed = %q, want 1.27rc1", got)
	}
}

func TestMiseInstallCoversPinsMiseReads(t *testing.T) {
	target, _ := goversion.Parse("1.26.9")
	plan := Plan{Target: target, Changes: []Change{
		{File: ".tool-versions", Kind: pins.KindToolVersions, To: "1.26"},
		{File: "Dockerfile", Kind: pins.KindDockerfile, To: "1.26.9"},
	}}
	if got := MiseInstall(plan); !slices.Equal(got, []string{"mise", "install", "go@1.26.9"}) {
		t.Errorf("MiseInstall = %q, want the exact target installed", got)
	}
	if got := MiseInstall(Plan{Target: target}); got != nil {
		t.Errorf("MiseInstall = %q, want nil with no changes", got)
	}
}

func TestExecRunnerDropsInheritedGOROOT(t *testing.T) {
	t.Setenv("GOROOT", "/leaked/toolchain")
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{nil, ""},
		{[]string{"GOROOT=/explicit"}, "/explicit"},
	} {
		var out strings.Builder
		run := ExecRunner{Stdout: &out}
		if err := run.Run(context.Background(), t.TempDir(), tc.env, "sh", "-c", `printf %s "$GOROOT"`); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Errorf("env %q: child GOROOT = %q, want %q", tc.env, out.String(), tc.want)
		}
	}
}
