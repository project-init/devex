package bufsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

type fakeRegistry map[string]string

func (f fakeRegistry) LatestPlugin(_ context.Context, plugin string) (string, error) {
	v, ok := f[plugin]
	if !ok {
		return "", fmt.Errorf("unexpected plugin %s", plugin)
	}

	return v, nil
}

// fakeRunner answers git ls-remote from a map of repository URL to tags.
type fakeRunner map[string][]string

func (f fakeRunner) Output(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	if name != "git" || len(args) != 5 || args[0] != "ls-remote" || args[3] != "--end-of-options" {
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}
	var out strings.Builder
	for _, tag := range f[args[4]] {
		fmt.Fprintf(&out, "0123abcd\trefs/tags/%s\n", tag)
	}

	return []byte(out.String()), nil
}

const appTemplate = `version: v2
# Protos live in the separate protos repo.
inputs:
  - git_repo: https://github.com/acme/protos.git
    tag: v1.8.2 # the release this app builds against
  - git_repo: https://github.com/acme/other.git
    branch: main
  - module: buf.build/acme/weather:v1.2.0
plugins:
  - remote: buf.build/apple/swift
    out: ios/Protos
  - remote: "buf.build/connectrpc/swift:v1.0.0"
    out: ios/Protos
`

const protosTemplate = `version: v2
inputs:
  - directory: protodef
plugins:
  - local: protoc-gen-go
    out: pkg
  - remote: buf.build/protocolbuffers/go:v1.36.5
    out: gen
  - remote: buf.build/bufbuild/es:v2.13.0
    out: gen/ts
`

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func newPlanner(t *testing.T, policies map[string]string) Planner {
	t.Helper()
	root := writeTree(t, map[string]string{
		"buf.gen.yaml":                  appTemplate,
		"protos/buf.gen.ts.yaml":        protosTemplate,
		"protos/buf.yaml":               "version: v2\ndeps:\n  - buf.build/bufbuild/protovalidate\n",
		"other/buf.yaml":                "version: v2\n",
		"testdata/buf.gen.yaml":         "version: v2\nplugins:\n  - remote: buf.build/acme/fixture:v0.0.1\n",
		"vendor/acme/buf.gen.yaml":      "version: v2\nplugins:\n  - remote: buf.build/acme/fixture:v0.0.1\n",
		"docs/not-a-template.yaml":      "plugins:\n  - remote: buf.build/acme/ignored:v0.0.1\n",
		"empty/buf.gen.yaml":            "version: v2\n",
		"protos/protodef/example.proto": "syntax = \"proto3\";\n",
	})

	return Planner{
		Root:      root,
		Policies:  policies,
		Registry:  fakeRegistry{"buf.build/connectrpc/swift": "v1.2.0", "buf.build/protocolbuffers/go": "v1.36.11", "buf.build/bufbuild/es": "v2.13.0"},
		Run:       fakeRunner{"https://github.com/acme/protos.git": {"v1.8.1", "v1.8.3", "v1.9.0", "v2.0.0", "v2.1.0-rc.1"}},
		ListFiles: pins.WalkFiles,
	}
}

func describe(changes []edit.Change) []string {
	var out []string
	for _, c := range changes {
		out = append(out, fmt.Sprintf("%s:%d %s→%s", c.File, c.Line, c.From, c.To))
	}

	return out
}

func TestPlanMovesPluginsAndTags(t *testing.T) {
	p := newPlanner(t, map[string]string{"https://github.com/acme/protos.git": "minor"})
	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"buf.gen.yaml:5 v1.8.2→v1.9.0",
		"buf.gen.yaml:12 v1.0.0→v1.2.0",
		"protos/buf.gen.ts.yaml:7 v1.36.5→v1.36.11",
	}
	if got := describe(plan.Changes); !slices.Equal(got, want) {
		t.Errorf("changes =\n%q\nwant\n%q", got, want)
	}
	if _, templates := plan.With(nil); !slices.Equal(templates, []string{"buf.gen.yaml", "protos/buf.gen.ts.yaml"}) {
		t.Errorf("templates = %q", templates)
	}
	if want := []string{"buf.gen.yaml", "empty/buf.gen.yaml", "protos/buf.gen.ts.yaml"}; !slices.Equal(plan.AllTemplates, want) {
		t.Errorf("all templates = %q, want %q", plan.AllTemplates, want)
	}
	if want := []string{"protos"}; !slices.Equal(plan.DepModules, want) {
		t.Errorf("dep modules = %q, want %q", plan.DepModules, want)
	}
	warnings := strings.Join(plan.Warnings, "\n")
	for _, want := range []string{"buf.gen.yaml:10: buf.build/apple/swift has no version", "https://github.com/acme/other.git has no tag"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}

	root := p.Root
	if err := edit.Apply(root, plan.Changes); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "buf.gen.yaml"))
	for _, want := range []string{"tag: v1.9.0 # the release this app builds against", `remote: "buf.build/connectrpc/swift:v1.2.0"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("buf.gen.yaml lacks %q:\n%s", want, data)
		}
	}
}

func TestPlanHonorsPinAndPatch(t *testing.T) {
	plan, err := newPlanner(t, map[string]string{
		"https://github.com/acme/protos.git": "patch",
		"buf.build/connectrpc/swift":         "pin",
		"buf.build/protocolbuffers/go":       "pin",
		"buf.build/apple/swift":              "pin",
	}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(plan.Changes), []string{"buf.gen.yaml:5 v1.8.2→v1.8.3"}; !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
	if strings.Contains(strings.Join(plan.Warnings, "\n"), "apple/swift") {
		t.Errorf("a pinned plugin without a version still warned: %q", plan.Warnings)
	}
}

func TestPlanTakesExactPluginVersionsAndSkipsRevisions(t *testing.T) {
	root := writeTree(t, map[string]string{"buf.gen.yaml": `version: v2
plugins:
  - remote: buf.build/protocolbuffers/python:v29.1
    out: py
  - remote: buf.build/bufbuild/es:v2.13.0
    revision: 2
    out: ts
`})
	reg := fakeRegistry{"buf.build/protocolbuffers/python": "v30.0.1"}
	plan, err := Planner{Root: root, Registry: reg, Run: fakeRunner{}, ListFiles: pins.WalkFiles}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := describe(plan.Changes), []string{"buf.gen.yaml:3 v29.1→v30.0.1"}; !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "buf.build/bufbuild/es pins a revision") {
		t.Errorf("warnings = %q", plan.Warnings)
	}
}

func TestPlanAcceptsPoliciesOnUnmanagedEntries(t *testing.T) {
	warnings := strings.Join(mustPlan(t, nil).Warnings, "\n")
	if !strings.Contains(warnings, "buf.build/acme/weather pins module input version v1.2.0") {
		t.Errorf("warnings lack the versioned module input:\n%s", warnings)
	}

	pinned := map[string]string{"https://github.com/acme/other.git": "pin", "buf.build/acme/weather": "pin"}
	warnings = strings.Join(mustPlan(t, pinned).Warnings, "\n")
	if strings.Contains(warnings, "acme/other.git") || strings.Contains(warnings, "acme/weather") {
		t.Errorf("pinned unmanaged entries still warned:\n%s", warnings)
	}
}

func TestPlanKeepsUnlocatablePinsKnown(t *testing.T) {
	root := writeTree(t, map[string]string{"buf.gen.yaml": "version: v2\ninputs:\n  - git_repo: https://github.com/acme/protos.git\n    tag: >-\n      v1.8.2\n"})
	p := Planner{Root: root, Run: fakeRunner{}, ListFiles: pins.WalkFiles}
	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "protos.git is written in a form devex cannot locate") {
		t.Errorf("warnings = %q", plan.Warnings)
	}

	// A pin policy still finds the entry, rather than failing as a policy naming nothing.
	p.Policies = map[string]string{"https://github.com/acme/protos.git": "pin"}
	if plan, err = p.Plan(context.Background()); err != nil || len(plan.Warnings) != 0 {
		t.Errorf("err = %v, warnings = %q; want neither", err, plan.Warnings)
	}
}

// stalledRunner blocks until its context ends, like a git host that never answers. It fails
// after 5s if no timeout ends it.
type stalledRunner struct{}

func (stalledRunner) Output(ctx context.Context, _ string, _ []string, _ string, _ ...string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, errors.New("signal: killed")
	case <-time.After(5 * time.Second):
		return nil, errors.New("no timeout applied")
	}
}

// failedRunner fails at once, like git refusing a repository.
type failedRunner struct{}

func (failedRunner) Output(context.Context, string, []string, string, ...string) ([]byte, error) {
	return nil, errors.New("exit status 128")
}

func TestPlanReportsTagListingFailures(t *testing.T) {
	root := writeTree(t, map[string]string{"buf.gen.yaml": "version: v2\ninputs:\n  - git_repo: https://github.com/acme/protos.git\n    tag: v1.8.2\n"})
	for _, tc := range []struct {
		name     string
		run      Runner
		timeout  time.Duration
		want     string
		deadline bool
	}{
		{"a stalled git times out", stalledRunner{}, time.Millisecond, "timed out after 1ms", true},
		{"git's own error survives the timeout", failedRunner{}, time.Minute, "exit status 128", false},
	} {
		_, err := Planner{Root: root, Run: tc.run, Timeout: tc.timeout, ListFiles: pins.WalkFiles}.Plan(context.Background())
		if err == nil || !strings.Contains(err.Error(), tc.want) || errors.Is(err, context.DeadlineExceeded) != tc.deadline {
			t.Errorf("%s: err = %v, want %q with deadline exceeded %t", tc.name, err, tc.want, tc.deadline)
		}
	}
}

func mustPlan(t *testing.T, policies map[string]string) Plan {
	t.Helper()
	plan, err := newPlanner(t, policies).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return plan
}

func TestPlanReadsV1PluginsPortsAndWarnsOnNonReleases(t *testing.T) {
	root := writeTree(t, map[string]string{"buf.gen.yaml": `version: v1
plugins:
  - plugin: go
    out: gen
  - plugin: buf.build/protocolbuffers/go:v1.31.0
    out: gen
  - remote: bsr.acme.dev:8443/acme/validate:v1.0.0
    out: gen
inputs:
  - git_repo: https://github.com/acme/monorepo.git
    tag: proto/v1.8.2
`})
	reg := fakeRegistry{"buf.build/protocolbuffers/go": "v1.36.11", "bsr.acme.dev:8443/acme/validate": "v1.1.0"}
	plan, err := Planner{Root: root, Registry: reg, Run: fakeRunner{}, ListFiles: pins.WalkFiles}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"buf.gen.yaml:5 v1.31.0→v1.36.11", "buf.gen.yaml:7 v1.0.0→v1.1.0"}
	if got := describe(plan.Changes); !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "is at tag proto/v1.8.2, which is not a release version") {
		t.Errorf("warnings = %q", plan.Warnings)
	}
}

func TestPlanRejectsBadPolicies(t *testing.T) {
	cases := map[string]map[string]string{
		"remote plugins take latest or pin": {"buf.build/protocolbuffers/go": "minor"},
		// An unversioned plugin still takes only latest or pin, so pinning it later cannot break
		// the config.
		"buf.policies.buf.build/apple/swift: remote plugins": {"buf.build/apple/swift": "minor"},
		"unknown policy":            {"buf.build/bufbuild/es": "hold"},
		"which no buf.gen template": {"https://github.com/acme/missing.git": "pin"},
	}
	for want, policies := range cases {
		if _, err := newPlanner(t, policies).Plan(context.Background()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("policies %v: err = %v, want %q", policies, err, want)
		}
	}
}

func TestPlanAsksTheRegistryOncePerPlugin(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/buf.gen.yaml": protosTemplate,
		"b/buf.gen.yaml": protosTemplate,
	})
	reg := &countingRegistry{fakeRegistry: fakeRegistry{"buf.build/protocolbuffers/go": "v1.36.11", "buf.build/bufbuild/es": "v2.13.0"}}
	plan, err := Planner{Root: root, Registry: reg, Run: fakeRunner{}, ListFiles: pins.WalkFiles}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 2 || reg.calls != 2 {
		t.Errorf("changes = %d, registry calls = %d; want 2 changes from 2 calls", len(plan.Changes), reg.calls)
	}
}

type countingRegistry struct {
	fakeRegistry
	calls int
}

func (c *countingRegistry) LatestPlugin(ctx context.Context, plugin string) (string, error) {
	c.calls++

	return c.fakeRegistry.LatestPlugin(ctx, plugin)
}

func TestGenerateCommand(t *testing.T) {
	if dir, cmd := GenerateCommand("buf.gen.yaml"); dir != "." || strings.Join(cmd, " ") != "buf generate" {
		t.Errorf("GenerateCommand(buf.gen.yaml) = %q, %q", dir, cmd)
	}
	if dir, cmd := GenerateCommand("protos/buf.gen.ts.yaml"); dir != "protos" || strings.Join(cmd, " ") != "buf generate --template buf.gen.ts.yaml" {
		t.Errorf("GenerateCommand(protos/buf.gen.ts.yaml) = %q, %q", dir, cmd)
	}
}

func TestModulePath(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/protos.git":     "github.com/acme/protos",
		"https://GitHub.com/acme/protos/":        "github.com/acme/protos",
		"ssh://git@github.com/acme/protos.git":   "github.com/acme/protos",
		"git@github.com:acme/protos.git":         "github.com/acme/protos",
		"https://git.acme.dev:8443/team/api.git": "git.acme.dev/team/api",
		"../protos":                              "",
		"/srv/git/protos.git":                    "",
		"file:///srv/git/protos.git":             "",
	}
	for repo, want := range cases {
		if got := modulePath(repo); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", repo, got, want)
		}
	}
}

const linkedTemplate = "version: v2\ninputs:\n  - git_repo: https://github.com/acme/protos.git\n    tag: v1.8.2\n"

func linkedPlanner(t *testing.T, goMod string, policies map[string]string) Planner {
	t.Helper()
	root := writeTree(t, map[string]string{"buf.gen.yaml": linkedTemplate, "go.mod": goMod})

	// failedRunner proves a linked input needs no tag listing.
	return Planner{Root: root, Policies: policies, Run: failedRunner{}, ListFiles: pins.WalkFiles}
}

func TestLinksFollowTheGoModuleOfTheSameRepository(t *testing.T) {
	for _, tc := range []struct{ name, require, to string }{
		{"same major", "github.com/acme/protos v1.9.0", "v1.9.0"},
		{"major suffix", "github.com/acme/protos/v2 v2.1.0", "v2.1.0"},
		{"two-digit major", "github.com/acme/protos/v12 v12.3.0", "v12.3.0"},
		{"prerelease", "github.com/acme/protos v1.9.0-rc.1", "v1.9.0-rc.1"},
	} {
		p := linkedPlanner(t, "module example.com/app\n\nrequire "+tc.require+"\n", nil)
		plan, err := p.Plan(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(plan.Changes) != 0 || len(plan.Links) != 1 {
			t.Errorf("%s: plan = %v, %+v; want only a link", tc.name, plan.Changes, plan.Links)
		}

		links, _, err := p.Links()
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || !links[0].Moves() || describe([]edit.Change{links[0].Change})[0] != "buf.gen.yaml:4 v1.8.2→"+tc.to {
			t.Errorf("%s: links = %+v", tc.name, links)
		}
	}
}

func TestLinksSkipAModuleOnAPseudoVersion(t *testing.T) {
	p := linkedPlanner(t, "module example.com/app\n\nrequire github.com/acme/protos v1.8.3-0.20260901000000-abcdef123456\n", nil)
	links, _, err := p.Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("links = %+v; want none, so the input keeps its policy", links)
	}
}

func TestPlanRejectsAPolicyOnALinkedInput(t *testing.T) {
	p := linkedPlanner(t, "module example.com/app\n\nrequire github.com/acme/protos v1.8.2\n", map[string]string{"https://github.com/acme/protos.git": "minor"})
	if _, err := p.Plan(context.Background()); err == nil || !strings.Contains(err.Error(), "follows github.com/acme/protos in go.mod") {
		t.Errorf("err = %v, want the link named", err)
	}
}

func TestPlanWarnsWhenAnInputMatchesSeveralModules(t *testing.T) {
	p := linkedPlanner(t, "module example.com/app\n\nrequire (\n\tgithub.com/acme/protos v1.8.2\n\tgithub.com/acme/protos/v2 v2.0.0\n)\n", nil)
	p.Run = fakeRunner{"https://github.com/acme/protos.git": {"v1.8.2"}}
	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	links, warnings, err := p.Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 || !slices.Equal(warnings, plan.Warnings) || len(warnings) != 1 || !strings.Contains(warnings[0], "links it to none") {
		t.Errorf("links = %+v, warnings = %q, plan warnings = %q", links, warnings, plan.Warnings)
	}
}

func TestLinksStopMovingOnceTheTagMatches(t *testing.T) {
	p := linkedPlanner(t, "module example.com/app\n\nrequire github.com/acme/protos v1.8.2\n", nil)
	links, _, err := p.Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Moves() || links[0].Required != "v1.8.2" {
		t.Errorf("links = %+v; want one link already in step", links)
	}
}

func TestLinksFollowWhatGoBuildsAgainst(t *testing.T) {
	cases := []struct{ name, goMod string }{
		{"version replace", "module example.com/app\n\nrequire github.com/acme/protos v1.8.2\n\nreplace github.com/acme/protos => github.com/acme/protos v1.9.0\n"},
		{"fork replace", "module example.com/app\n\nrequire github.com/acme/protos v1.8.2\n\nreplace github.com/acme/protos => github.com/fork/protos v1.9.5\n"},
		{"local replace", "module example.com/app\n\nrequire github.com/acme/protos v1.9.0\n\nreplace github.com/acme/protos => ../protos\n"},
		{"exact beats wildcard", "module example.com/app\n\nrequire github.com/acme/protos v1.8.2\n\nreplace github.com/acme/protos => ../protos\n\nreplace github.com/acme/protos v1.8.2 => github.com/acme/protos v1.9.0\n"},
	}
	want := map[string]string{"version replace": "v1.9.0", "fork replace": "", "local replace": "", "exact beats wildcard": "v1.9.0"}
	for _, tc := range cases {
		links, _, err := linkedPlanner(t, tc.goMod, nil).Links()
		if err != nil {
			t.Fatal(err)
		}
		if to := want[tc.name]; to == "" && len(links) != 0 || to != "" && (len(links) != 1 || links[0].Change.To != to) {
			t.Errorf("%s: links = %+v; want the tag moving to %q, or no link for \"\"", tc.name, links, to)
		}
	}
}

func TestLinksSkipGoModulesGoExcludes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"buf.gen.yaml":         linkedTemplate,
		"go.mod":               "module example.com/app\n\nrequire github.com/acme/protos v1.8.2\n",
		"examples/demo/go.mod": "module example.com/demo\n\nrequire github.com/acme/protos v1.9.0\n",
	})
	p := Planner{Root: root, ListFiles: pins.WalkFiles, GoExclude: []string{"examples/"}}
	links, _, err := p.Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Required != "v1.8.2" || links[0].Moves() {
		t.Errorf("links = %+v; want the excluded examples module ignored", links)
	}
}

func TestWithLetsALinkReplaceAPlannedMoveOnTheSameSpan(t *testing.T) {
	planned := edit.Change{File: "buf.gen.yaml", Line: 4, Span: pins.Span{Start: 78, End: 84}, From: "v1.8.2", To: "v1.9.0"}
	link := Link{Change: edit.Change{File: "buf.gen.yaml", Line: 4, Span: planned.Span, From: "v1.8.2", To: "v1.9.1"}, Module: "github.com/acme/protos"}
	changes, templates := Plan{Changes: []edit.Change{planned}}.With([]Link{link})
	if len(changes) != 1 || changes[0].To != "v1.9.1" || !slices.Equal(templates, []string{"buf.gen.yaml"}) {
		t.Errorf("With = %v, %q; want only the link's move", changes, templates)
	}
}

func TestLinksSkipATemplateTheyCannotParse(t *testing.T) {
	root := writeTree(t, map[string]string{
		"buf.gen.yaml":          linkedTemplate,
		"go.mod":                "module example.com/app\n\nrequire github.com/acme/protos v1.9.0\n",
		"examples/buf.gen.yaml": "version: v2\nplugins:\n  - remote: [unclosed\n",
	})
	links, warnings, err := Planner{Root: root, ListFiles: pins.WalkFiles}.Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "examples/buf.gen.yaml") {
		t.Errorf("links = %+v, warnings = %q; want the stray template skipped with a warning", links, warnings)
	}
}

func TestWithDropsAPlannedMoveUnderALinkThatStays(t *testing.T) {
	span := pins.Span{Start: 78, End: 84}
	planned := edit.Change{File: "buf.gen.yaml", Line: 4, Span: span, From: "v1.8.2", To: "v1.9.0"}
	link := Link{Change: edit.Change{File: "buf.gen.yaml", Line: 4, Span: span, From: "v1.8.2", To: "v1.8.2"}, Module: "github.com/acme/protos"}
	changes, templates := Plan{Changes: []edit.Change{planned}}.With([]Link{link})
	if len(changes) != 0 || len(templates) != 0 {
		t.Errorf("With = %v, %q; want the planned move dropped and nothing to regenerate", changes, templates)
	}
}

func TestPlanFailsOnAGoModItCannotParse(t *testing.T) {
	p := linkedPlanner(t, "module example.com/app\n\nfrobnicate everything\n", nil)
	if _, err := p.Plan(context.Background()); err == nil || !strings.Contains(err.Error(), "parse go.mod") {
		t.Errorf("err = %v, want the go.mod parse error", err)
	}
}

func TestLinksLeaveAnInputUnlinkedBehindANewerPseudoVersion(t *testing.T) {
	root := writeTree(t, map[string]string{
		"buf.gen.yaml": linkedTemplate,
		"go.mod":       "module example.com/app\n\nrequire github.com/acme/protos v1.9.0\n",
		"tools/go.mod": "module example.com/tools\n\nrequire github.com/acme/protos v1.9.1-0.20260901000000-abcdef123456\n",
	})
	links, _, err := Planner{Root: root, ListFiles: pins.WalkFiles}.Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("links = %+v; want the newer pseudo-version to win and leave the input unlinked", links)
	}
}

func TestLinksIgnoreIndirectRequirements(t *testing.T) {
	links, _, err := linkedPlanner(t, "module example.com/app\n\nrequire github.com/acme/protos v1.5.0 // indirect\n", nil).Links()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("links = %+v; want an indirect requirement ignored", links)
	}
}
