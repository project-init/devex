package dependencies

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/bufsync"
	"github.com/project-init/devex/internal/sre/dependencies/gosync"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/misesync"
	"github.com/project-init/devex/internal/sre/dependencies/registry"
	"github.com/project-init/devex/internal/ui"
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

type fakeRegistry map[string]string

func (f fakeRegistry) Digest(_ context.Context, image, tag string) (string, error) {
	if d, ok := f[image+":"+tag]; ok {
		return d, nil
	}

	return "", registry.ErrNotFound
}

// recordingRunner records each command, and answers Output from outputs, keyed by the command
// line without its directory.
type recordingRunner struct {
	root    string
	calls   []string
	outputs map[string]string
	// effects runs after a command, keyed by the command line without its directory.
	effects map[string]func(dir string)
}

func (r *recordingRunner) Run(_ context.Context, dir string, env []string, name string, args ...string) error {
	rel, _ := filepath.Rel(r.root, dir)
	r.calls = append(r.calls, strings.Join(slices.Concat([]string{"[" + rel + "]"}, env, []string{name}, args), " "))
	if effect := r.effects[strings.Join(slices.Concat([]string{name}, args), " ")]; effect != nil {
		effect(dir)
	}

	return nil
}

func (r *recordingRunner) Output(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	command := strings.Join(slices.Concat([]string{name}, args), " ")
	out, ok := r.outputs[command]
	if !ok {
		return nil, fmt.Errorf("unexpected command %q", command)
	}

	return []byte(out), nil
}

// testUI writes data, status, and warnings alike to w.
func testUI(w io.Writer) *ui.UI {
	return ui.NewWithStreams(nil, w, w, ui.Options{})
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}

	return dst
}

func assertTree(t *testing.T, got, want string) {
	t.Helper()
	err := filepath.WalkDir(want, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(want, p)
		wantData, _ := os.ReadFile(p)
		gotData, err := os.ReadFile(filepath.Join(got, rel))
		if err != nil {
			return err
		}
		if !bytes.Equal(gotData, wantData) {
			t.Errorf("%s differs from golden:\n--- got\n%s\n--- want\n%s", rel, gotData, wantData)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type fakePlugins map[string]string

func (f fakePlugins) LatestPlugin(_ context.Context, plugin string) (string, error) {
	if v, ok := f[plugin]; ok {
		return v, nil
	}

	return "", fmt.Errorf("unexpected plugin %s", plugin)
}

func testEnvironment(root string) (environment, *recordingRunner) {
	run := &recordingRunner{root: root, outputs: map[string]string{
		"mise --version":        "2026.6.12 macos-arm64 (2026-06-21)\n",
		"mise config ls --json": fmt.Sprintf(`[{"path": %q}]`, filepath.Join(root, "mise.toml")),
		"mise latest node@26":   "26.10.0\n",
		"git ls-remote --tags --refs --end-of-options https://github.com/acme/protos.git": "0a1\trefs/tags/v1.8.2\n0b2\trefs/tags/v1.9.0\n0c3\trefs/tags/v2.0.0\n",
	}}

	return environment{
		Root:     root,
		Run:      run,
		Resolver: fakeResolver{"1.27.1", "1.26.9", "1.26.6"},
		Registry: fakeRegistry{"golang:1.26.9-alpine3.24": "sha256:abc"},
		Plugins:  fakePlugins{"buf.build/protocolbuffers/go": "v1.36.11"},
		HasMise:  true,
	}, run
}

var exactConfig = config.DependenciesConfiguration{Go: config.GoDependenciesConfiguration{Directive: "exact"}}

// policyConfig caps node and the protos input at their majors and pins awscli.
var policyConfig = config.DependenciesConfiguration{
	Go:   config.GoDependenciesConfiguration{Directive: "exact"},
	Mise: config.PolicyConfiguration{Policies: map[string]string{"node": "minor", "awscli": "pin"}},
	Buf:  config.PolicyConfiguration{Policies: map[string]string{"https://github.com/acme/protos.git": "minor"}},
}

func TestUpgradeMatchesGoldenTree(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	var out bytes.Buffer

	o := upgradeOptions{goFlag: true, miseFlag: true, bufFlag: true}
	if err := runUpgrade(context.Background(), testUI(&out), o, policyConfig, env); err != nil {
		t.Fatalf("runUpgrade: %v\n%s", err, out.String())
	}

	assertTree(t, root, "testdata/upgrade/golden")
	want := []string{
		"[.] mise install go@1.26.9",
		"[.] mise upgrade node",
		"[.] mise upgrade --bump --exclude awscli --exclude go --exclude node",
		"[.] GOTOOLCHAIN=go1.26.9 GOWORK=off go get example.com/lib@upgrade",
		"[.] GOTOOLCHAIN=go1.26.9 GOWORK=off go mod tidy",
		"[.] buf dep update",
		"[.] buf generate",
	}
	if strings.Join(run.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(run.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestUpgradeMiseAloneLeavesGoPut(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)

	if err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), upgradeOptions{miseFlag: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}

	assertTree(t, root, "testdata/upgrade/input")
	if want := "[.] mise upgrade --bump --exclude go"; strings.Join(run.calls, "\n") != want {
		t.Errorf("calls = %q, want only %q", run.calls, want)
	}
}

func TestUpgradeStopsOnAnOldMiseBeforeWriting(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	run.outputs["mise --version"] = "2025.12.13 macos-arm64 (2025-12-30)\n"

	o := upgradeOptions{goFlag: true, miseFlag: true}
	err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), o, policyConfig, env)
	if err == nil || !strings.Contains(err.Error(), "mise 2025.12.13 is too old") {
		t.Fatalf("err = %v, want mise too old", err)
	}
	assertTree(t, root, "testdata/upgrade/input")
	if len(run.calls) != 0 {
		t.Errorf("ran %q before stopping", run.calls)
	}
}

func TestUpgradeDryRunWritesNothing(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	var out bytes.Buffer

	if err := runUpgrade(context.Background(), testUI(&out), upgradeOptions{goFlag: true, dryRun: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}

	assertTree(t, root, "testdata/upgrade/input")
	if len(run.calls) != 0 {
		t.Errorf("dry run ran %q", run.calls)
	}
	for _, want := range []string{"Go 1.26.6 → 1.26.9", "docker/api/Dockerfile:7", "mise.toml:5", "GOTOOLCHAIN=go1.26.9", "mise install go@1.26.9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestUpgradeDryRunShowsMiseAndBufPlans(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	var out bytes.Buffer

	o := upgradeOptions{miseFlag: true, bufFlag: true, dryRun: true}
	if err := runUpgrade(context.Background(), testUI(&out), o, policyConfig, env); err != nil {
		t.Fatal(err)
	}

	assertTree(t, root, "testdata/upgrade/input")
	if len(run.calls) != 0 {
		t.Errorf("dry run ran %q", run.calls)
	}
	for _, want := range []string{
		"mise.toml:7  26.8.2 → 26.10.0",
		"mise upgrade node\nmise upgrade --bump --exclude awscli --exclude go --exclude node\n",
		"buf.gen.yaml:4  v1.8.2 → v1.9.0",
		"buf.gen.yaml:6  v1.36.5 → v1.36.11",
		"buf generate  (in .)",
		"buf dep update  (in .)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestUpgradeRegeneratesEveryTemplateWhenALockMoves(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	if err := os.MkdirAll(filepath.Join(root, "gen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gen", "buf.gen.yaml"), []byte("version: v2\ninputs:\n  - directory: ..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, run := testEnvironment(root)
	run.effects = map[string]func(string){"buf dep update": func(dir string) {
		_ = os.WriteFile(filepath.Join(dir, "buf.lock"), []byte("version: v2\n"), 0o644)
	}}

	if err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), upgradeOptions{bufFlag: true}, policyConfig, env); err != nil {
		t.Fatal(err)
	}
	want := "[.] buf dep update\n[.] buf generate\n[gen] buf generate"
	if strings.Join(run.calls, "\n") != want {
		t.Errorf("calls = %q, want %q", run.calls, want)
	}
}

func TestUpgradeStopsBeforeWritingWhenAPolicyIsWrong(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	cfg := policyConfig
	cfg.Buf.Policies = map[string]string{"buf.build/protocolbuffers/go": "minor"}

	o := upgradeOptions{goFlag: true, miseFlag: true, bufFlag: true}
	if err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), o, cfg, env); err == nil || !strings.Contains(err.Error(), "remote plugins take latest or pin") {
		t.Fatalf("err = %v, want the plugin policy rejected", err)
	}
	assertTree(t, root, "testdata/upgrade/input")
	if len(run.calls) != 0 {
		t.Errorf("ran %q after a failed plan", run.calls)
	}
}

func TestUpgradeWithoutMiseSkipsInstall(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	env.HasMise = false
	var out bytes.Buffer

	if err := runUpgrade(context.Background(), testUI(&out), upgradeOptions{goFlag: true, dryRun: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "mise install") {
		t.Errorf("dry run lists mise install without mise on PATH:\n%s", out.String())
	}
	if err := runUpgrade(context.Background(), testUI(&out), upgradeOptions{goFlag: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(run.calls, func(c string) bool { return strings.Contains(c, "mise install") }) {
		t.Errorf("calls = %q, want no mise install without mise on PATH", run.calls)
	}
}

func TestUpgradeStopsBeforeWritingWhenTagMissing(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	env.Registry = fakeRegistry{}

	err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), upgradeOptions{goFlag: true, miseFlag: true}, exactConfig, env)
	if err == nil || !strings.Contains(err.Error(), "golang:1.26.9-alpine3.24 is not published") {
		t.Fatalf("err = %v, want the missing tag named", err)
	}
	assertTree(t, root, "testdata/upgrade/input")
	if len(run.calls) != 0 {
		t.Errorf("ran %q after a failed plan", run.calls)
	}
}

func TestCheckPassesOnGoldenAndFailsOnDrift(t *testing.T) {
	var out bytes.Buffer
	if err := runCheck(testUI(&out), exactConfig, copyTree(t, "testdata/upgrade/golden")); err != nil {
		t.Fatalf("check on golden tree: %v", err)
	}
	if !strings.Contains(out.String(), "Go 1.26.9: 4 pins in step") {
		t.Errorf("output = %q", out.String())
	}

	drifted := copyTree(t, "testdata/upgrade/golden")
	if err := os.WriteFile(filepath.Join(drifted, "mise.toml"), []byte("[tools]\ngo = \"1.27.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(testUI(&bytes.Buffer{}), exactConfig, drifted); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Errorf("err = %v, want drift", err)
	}
}

func TestUpgradeMovesALinkedTagWhereGoGetLeftItsModule(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	goMod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/app\n\ngo 1.26.6\n\nrequire github.com/acme/protos v1.8.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, run := testEnvironment(root)
	run.effects = map[string]func(string){"go get github.com/acme/protos@upgrade": func(string) {
		_ = os.WriteFile(goMod, []byte("module example.com/app\n\ngo 1.26.9\n\nrequire github.com/acme/protos v1.9.0\n"), 0o644)
	}}

	if err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), upgradeOptions{goFlag: true, bufFlag: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "buf.gen.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tag: v1.9.0 # the release this app builds against") {
		t.Errorf("buf.gen.yaml =\n%s\nwant the tag at the go.mod version", data)
	}
	for _, call := range run.calls {
		if strings.Contains(call, "ls-remote") {
			t.Errorf("listed tags of a linked input: %q", call)
		}
	}
}

func TestCheckFailsWhenALinkedTagDriftsFromGoMod(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/golden")
	goMod := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, append(data, "\nrequire github.com/acme/protos v1.9.1\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(testUI(&bytes.Buffer{}), exactConfig, root); err == nil || !strings.Contains(err.Error(), "go.mod builds against github.com/acme/protos v1.9.1") {
		t.Errorf("err = %v, want the linked tag's drift", err)
	}
}

func TestUpgradeWithoutGoIgnoresGoConfig(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	badGo := config.DependenciesConfiguration{Go: config.GoDependenciesConfiguration{Target: "nightly"}}

	if err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), upgradeOptions{bufFlag: true}, badGo, env); err != nil {
		t.Fatalf("--buf failed on unrelated Go config: %v", err)
	}
	if want := "[.] buf dep update\n[.] buf generate"; strings.Join(run.calls, "\n") != want {
		t.Errorf("calls = %q, want %q", run.calls, want)
	}
}

func TestEnableReadsUpgradeList(t *testing.T) {
	var o upgradeOptions
	if err := o.enable([]string{"mise", "go"}); err != nil {
		t.Fatal(err)
	}
	if !o.goFlag || !o.miseFlag || o.bufFlag {
		t.Errorf("options = %+v, want go and mise", o)
	}
	if err := (&upgradeOptions{}).enable(nil); err == nil {
		t.Error("an empty upgrade list was accepted")
	}
	if err := (&upgradeOptions{}).enable([]string{"npm"}); err == nil {
		t.Error("an unknown ecosystem was accepted")
	}
}

func TestCheckRejectsAPolicyOnALinkedInput(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/golden")
	goMod := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, append(data, "\nrequire github.com/acme/protos v1.9.0\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := exactConfig
	cfg.Buf.Policies = map[string]string{"https://github.com/acme/protos.git": "pin"}
	if err := runCheck(testUI(&bytes.Buffer{}), cfg, root); err == nil || !strings.Contains(err.Error(), "takes no policy") {
		t.Errorf("err = %v, want the linked policy rejected", err)
	}
}

func TestUpgradeGoAloneWarnsWhenALinkedTagTrails(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	goMod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/app\n\ngo 1.26.6\n\nrequire github.com/acme/protos v1.8.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, run := testEnvironment(root)
	run.effects = map[string]func(string){"go get github.com/acme/protos@upgrade": func(string) {
		_ = os.WriteFile(goMod, []byte("module example.com/app\n\ngo 1.26.9\n\nrequire github.com/acme/protos v1.9.0\n"), 0o644)
	}}

	var out bytes.Buffer
	if err := runUpgrade(context.Background(), testUI(&out), upgradeOptions{goFlag: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "warning: buf.gen.yaml:4: tag v1.8.2, but go.mod builds against github.com/acme/protos v1.9.0; run upgrade --buf") {
		t.Errorf("output =\n%s\nwant the trailing tag named", out.String())
	}
}

func TestHeldWarningExplainsEachHold(t *testing.T) {
	target, _ := goversion.Parse("1.26.8")
	for _, tc := range []struct {
		held gosync.Held
		want string
	}{
		{
			held: gosync.Held{Module: "k8s.io/kube-openapi", Version: "v0.0.0-1", Wanted: "v0.0.0-2", NeedsGo: "1.27.0"},
			want: "held k8s.io/kube-openapi at v0.0.0-1; v0.0.0-2 needs Go 1.27.0, above 1.26.8",
		},
		{
			held: gosync.Held{Module: "example.com/a", Version: "v1.0.0", Wanted: "v1.3.0", NeedsGo: "1.27.0", Via: "example.com/b", ViaVersion: "v1.1.0"},
			want: "held example.com/a at v1.0.0; v1.3.0 needs example.com/b v1.1.0, and example.com/b is held for Go 1.27.0, above 1.26.8",
		},
	} {
		if got := heldWarning(tc.held, target); got != tc.want {
			t.Errorf("heldWarning(%+v) = %q, want %q", tc.held, got, tc.want)
		}
	}
}

func TestCheckJSONListsEveryProblem(t *testing.T) {
	// Go drift in mise.toml, a policy on a linked input, and that input's tag off go.mod's version.
	root := copyTree(t, "testdata/upgrade/golden")
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tools]\ngo = \"1.27.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, append(data, "\nrequire github.com/acme/protos v1.9.1\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := exactConfig
	cfg.Buf.Policies = map[string]string{"https://github.com/acme/protos.git": "pin"}

	var out, errOut bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &errOut, ui.Options{Format: ui.FormatJSON})
	err = runCheck(u, cfg, root)
	var report checkReport
	if jsonErr := json.Unmarshal(out.Bytes(), &report); jsonErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, out.String())
	}
	checks := map[string]bool{}
	for _, p := range report.Problems {
		checks[p.Check] = true
	}
	for _, want := range []string{"go version drift: toolchain pins disagree", "linked buf.gen input takes a policy", "buf.gen git input out of step with go.mod"} {
		if !checks[want] {
			t.Errorf("problems lack %q: %+v", want, report.Problems)
		}
	}
	if report.Go != nil || err == nil || !strings.Contains(err.Error(), "dependency problems") {
		t.Errorf("go = %+v, err = %v; want no agreed version and a failing exit", report.Go, err)
	}
}

func TestCheckJSONOnAHealthyTree(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{Format: ui.FormatJSON})
	if err := runCheck(u, exactConfig, copyTree(t, "testdata/upgrade/golden")); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"problems": []`) || !strings.Contains(got, `"version": "1.26.9"`) {
		t.Errorf("stdout = %s, want an empty problem list and the agreed version", got)
	}
}

func TestDryRunJSONIsThePlan(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	var out bytes.Buffer
	u := ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{Format: ui.FormatJSON})
	o := upgradeOptions{goFlag: true, miseFlag: true, bufFlag: true, dryRun: true}
	if err := runUpgrade(context.Background(), u, o, policyConfig, env); err != nil {
		t.Fatal(err)
	}
	var doc planJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if doc.Go == nil || doc.Go.Current != "1.26.6" || doc.Go.Target != "1.26.9" || len(doc.Go.Changes) == 0 {
		t.Errorf("go = %+v, want 1.26.6 → 1.26.9 with changes", doc.Go)
	}
	if doc.Mise == nil || len(doc.Mise.Commands) != 2 || doc.Buf == nil || len(doc.Buf.Templates) == 0 {
		t.Errorf("mise = %+v, buf = %+v; want the capped and bump commands and the templates", doc.Mise, doc.Buf)
	}
	if len(run.calls) != 0 {
		t.Errorf("dry run ran %q", run.calls)
	}
}

func TestPlanWarningsGoToStderrInEcosystemOrder(t *testing.T) {
	var out, errOut bytes.Buffer
	p := plans{mise: misesync.Plan{Warnings: []string{"mise warns"}}, buf: bufsync.Plan{Warnings: []string{"buf warns"}}}
	p.goPlan.Warnings = []string{"go warns"}
	printWarnings(ui.NewWithStreams(nil, &out, &errOut, ui.Options{}), p.warnings())
	if out.Len() != 0 || errOut.String() != "warning: go warns\nwarning: mise warns\nwarning: buf warns\n" {
		t.Errorf("stdout %q, stderr %q; want the warnings on stderr, go then mise then buf", out.String(), errOut.String())
	}
}

// failingResolver stands in for go.dev being down.
type failingResolver struct{}

func (failingResolver) Releases(context.Context) ([]goversion.Version, error) {
	return nil, errors.New("go.dev is down")
}

func TestPlanReportsTheFirstEcosystemsFailure(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	env.Resolver = failingResolver{}
	delete(run.outputs, "mise --version") // mise fails too
	err := runUpgrade(context.Background(), testUI(&bytes.Buffer{}), upgradeOptions{goFlag: true, miseFlag: true, dryRun: true}, policyConfig, env)
	if err == nil || !strings.Contains(err.Error(), "go.dev is down") {
		t.Errorf("err = %v, want go's failure, reported before mise's", err)
	}
}

func TestCheckPrintsWarningsBeforeALinkFailure(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/golden")
	// An alias the finders cannot read warns; an unreadable buf.gen.yaml fails the link check,
	// which alone reads it.
	if err := os.WriteFile(filepath.Join(root, ".mise.toml"), []byte("[tools]\ngolang = \"1.26.9\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "buf.gen.yaml"), 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runCheck(testUI(&out), exactConfig, root)
	if err == nil || !strings.Contains(out.String(), "warning: .mise.toml") {
		t.Errorf("err = %v, output %q; want the failure and the warning found before it", err, out.String())
	}
}

func TestCheckJSONReportsALinkFailureAlongsideGoProblems(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/golden")
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tools]\ngo = \"1.27.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "buf.gen.yaml"), 0); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runCheck(ui.NewWithStreams(nil, &out, &bytes.Buffer{}, ui.Options{Format: ui.FormatJSON}), exactConfig, root)
	var report checkReport
	if jsonErr := json.Unmarshal(out.Bytes(), &report); jsonErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, out.String())
	}
	checks := map[string]bool{}
	for _, p := range report.Problems {
		checks[p.Check] = true
	}
	if !checks["read buf.gen inputs"] || !checks["go version drift: toolchain pins disagree"] || err == nil {
		t.Errorf("problems = %+v, err = %v; want the link failure beside the Go drift", report.Problems, err)
	}
}
