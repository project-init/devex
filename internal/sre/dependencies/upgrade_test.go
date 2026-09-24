package dependencies

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
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

type fakeRegistry map[string]string

func (f fakeRegistry) Digest(_ context.Context, image, tag string) (string, error) {
	if d, ok := f[image+":"+tag]; ok {
		return d, nil
	}

	return "", registry.ErrNotFound
}

// recordingRunner records each command.
type recordingRunner struct {
	root  string
	calls []string
}

func (r *recordingRunner) Run(_ context.Context, dir string, env []string, name string, args ...string) error {
	rel, _ := filepath.Rel(r.root, dir)
	r.calls = append(r.calls, strings.Join(slices.Concat([]string{"[" + rel + "]"}, env, []string{name}, args), " "))

	return nil
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

func testEnvironment(root string) (environment, *recordingRunner) {
	run := &recordingRunner{root: root}

	return environment{
		Root:     root,
		Run:      run,
		Resolver: fakeResolver{"1.27.1", "1.26.9", "1.26.6"},
		Registry: fakeRegistry{"golang:1.26.9-alpine3.24": "sha256:abc"},
		HasMise:  true,
	}, run
}

var exactConfig = config.DependenciesConfiguration{Go: config.GoDependenciesConfiguration{Directive: "exact"}}

func TestUpgradeMatchesGoldenTree(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	var out bytes.Buffer

	o := upgradeOptions{goFlag: true, miseFlag: true, bufFlag: true}
	if err := runUpgrade(context.Background(), &out, o, exactConfig, env); err != nil {
		t.Fatalf("runUpgrade: %v\n%s", err, out.String())
	}

	assertTree(t, root, "testdata/upgrade/golden")
	want := []string{
		"[.] mise install go@1.26.9",
		"[.] mise upgrade --bump --exclude go",
		"[.] GOTOOLCHAIN=go1.26.9 GOWORK=off go get -u ./...",
		"[.] GOTOOLCHAIN=go1.26.9 GOWORK=off go mod tidy",
		"[.] buf dep update",
	}
	if strings.Join(run.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(run.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestUpgradeMiseAloneLeavesGoPut(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)

	if err := runUpgrade(context.Background(), &bytes.Buffer{}, upgradeOptions{miseFlag: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}

	assertTree(t, root, "testdata/upgrade/input")
	if want := "[.] mise upgrade --bump --exclude go"; strings.Join(run.calls, "\n") != want {
		t.Errorf("calls = %q, want only %q", run.calls, want)
	}
}

func TestUpgradeDryRunWritesNothing(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	var out bytes.Buffer

	if err := runUpgrade(context.Background(), &out, upgradeOptions{goFlag: true, dryRun: true}, exactConfig, env); err != nil {
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

func TestUpgradeWithoutMiseSkipsInstall(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	env.HasMise = false
	var out bytes.Buffer

	if err := runUpgrade(context.Background(), &out, upgradeOptions{goFlag: true, dryRun: true}, exactConfig, env); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "mise install") {
		t.Errorf("dry run lists mise install without mise on PATH:\n%s", out.String())
	}
	if err := runUpgrade(context.Background(), &out, upgradeOptions{goFlag: true}, exactConfig, env); err != nil {
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

	err := runUpgrade(context.Background(), &bytes.Buffer{}, upgradeOptions{goFlag: true, miseFlag: true}, exactConfig, env)
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
	if err := runCheck(&out, exactConfig, copyTree(t, "testdata/upgrade/golden")); err != nil {
		t.Fatalf("check on golden tree: %v", err)
	}
	if !strings.Contains(out.String(), "Go 1.26.9: 4 pins in step") {
		t.Errorf("output = %q", out.String())
	}

	drifted := copyTree(t, "testdata/upgrade/golden")
	if err := os.WriteFile(filepath.Join(drifted, "mise.toml"), []byte("[tools]\ngo = \"1.27.1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCheck(&bytes.Buffer{}, exactConfig, drifted); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Errorf("err = %v, want drift", err)
	}
}

func TestUpgradeWithoutGoIgnoresGoConfig(t *testing.T) {
	root := copyTree(t, "testdata/upgrade/input")
	env, run := testEnvironment(root)
	badGo := config.DependenciesConfiguration{Go: config.GoDependenciesConfiguration{Target: "nightly"}}

	if err := runUpgrade(context.Background(), &bytes.Buffer{}, upgradeOptions{bufFlag: true}, badGo, env); err != nil {
		t.Fatalf("--buf failed on unrelated Go config: %v", err)
	}
	if strings.Join(run.calls, "\n") != "[.] buf dep update" {
		t.Errorf("calls = %q", run.calls)
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
