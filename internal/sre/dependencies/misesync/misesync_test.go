package misesync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

// fakeRunner answers mise config ls with configs, as absolute paths, and mise latest from a map
// of query to version, counting mise latest calls.
type fakeRunner struct {
	configs []string
	latest  map[string]string
	calls   int
}

func (f *fakeRunner) Output(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	switch command := name + " " + strings.Join(args, " "); {
	case command == "mise config ls --json":
		var listed []map[string]string
		for _, c := range f.configs {
			listed = append(listed, map[string]string{"path": c})
		}

		return json.Marshal(listed)
	case strings.HasPrefix(command, "mise latest "):
		f.calls++
		v, ok := f.latest[args[1]]
		if !ok {
			return nil, fmt.Errorf("unexpected query %s", args[1])
		}

		return []byte(v + "\n"), nil
	default:
		return nil, fmt.Errorf("unexpected command %s", command)
	}
}

const miseToml = `[tools]
go = "1.26.6"
node = "26.8.2" # LTS line
ruby = "4.0.6"
awscli = { version = "2.36.43", symlink_bins = "true" }
"go:github.com/bufbuild/buf/cmd/buf" = "v1.72.0"
prettier = "3.9.6"
air = "latest"
python = "3.14"
`

// planner writes files under a fresh root and reports the named ones as the configs mise loads,
// alongside a global config outside the repository.
func planner(t *testing.T, files map[string]string, loaded []string, policies map[string]string, latest map[string]string) (Planner, *fakeRunner) {
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
	global := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(global, []byte("[tools]\nbun = \"1.3.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{configs: []string{global, filepath.Join(root, ".tool-versions")}, latest: latest}
	for _, name := range loaded {
		run.configs = append(run.configs, filepath.Join(root, filepath.FromSlash(name)))
	}

	return Planner{Root: root, Policies: policies, Run: run, ListFiles: pins.WalkFiles}, run
}

func rootPlanner(t *testing.T, policies map[string]string, latest map[string]string) Planner {
	t.Helper()
	p, _ := planner(t, map[string]string{"mise.toml": miseToml, "frontend/mise.toml": "[tools]\nbun = \"1.3.0\"\n"}, []string{"mise.toml"}, policies, latest)

	return p
}

func TestPlanCapsExcludesAndLeavesLatestToMise(t *testing.T) {
	p := rootPlanner(t, map[string]string{
		"node":                               "minor",
		"awscli":                             "patch",
		"go:github.com/bufbuild/buf/cmd/buf": "minor",
		"ruby":                               "pin",
		"python":                             "minor",
	}, map[string]string{
		"node@26":                              "26.10.0",
		"awscli@2.36":                          "2.36.50",
		"go:github.com/bufbuild/buf/cmd/buf@1": "1.73.0",
		// A newer patch leaves a two-component pin as written.
		"python@3": "3.14.2",
	})

	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, c := range plan.Changes {
		got = append(got, fmt.Sprintf("%s:%d %s→%s", c.File, c.Line, c.From, c.To))
	}
	want := []string{"mise.toml:3 26.8.2→26.10.0", "mise.toml:5 2.36.43→2.36.50", "mise.toml:6 v1.72.0→v1.73.0"}
	if !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
	wantExclude := []string{"awscli", "go", "go:github.com/bufbuild/buf/cmd/buf", "node", "python", "ruby"}
	if !slices.Equal(plan.Exclude, wantExclude) {
		t.Errorf("exclude = %q, want %q", plan.Exclude, wantExclude)
	}
	if cmd := strings.Join(plan.UpgradeCommand(), " "); !strings.HasPrefix(cmd, "mise upgrade --bump --exclude awscli --exclude go ") {
		t.Errorf("UpgradeCommand = %q", cmd)
	}
	if cmd := strings.Join(plan.CapCommand(), " "); cmd != "mise upgrade awscli go:github.com/bufbuild/buf/cmd/buf node python" {
		t.Errorf("CapCommand = %q, want a plain upgrade of every capped tool", cmd)
	}
}

func TestPlanWarnsWhenAPolicyCannotCap(t *testing.T) {
	plan, err := rootPlanner(t, map[string]string{"air": "minor"}, nil).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], `air = "latest" is not a release version`) {
		t.Errorf("warnings = %q", plan.Warnings)
	}
}

func TestPlanKeepsBroadPinsPinned(t *testing.T) {
	p, _ := planner(t, map[string]string{"mise.toml": "[tools]\nnode = \"26\"\nruby = \"4.0\"\n"}, []string{"mise.toml"}, map[string]string{"node": "patch", "ruby": "patch"}, nil)
	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], `node = "26" spans more than its patch policy allows`) {
		t.Errorf("warnings = %q", plan.Warnings)
	}
	if want := []string{"ruby"}; !slices.Equal(plan.Capped, want) {
		t.Errorf("capped = %q, want %q: node stays pinned, ruby = 4.0 already names its patch range", plan.Capped, want)
	}
	if want := []string{"go", "node", "ruby"}; !slices.Equal(plan.Exclude, want) {
		t.Errorf("exclude = %q, want %q", plan.Exclude, want)
	}
}

func TestPlanKeepsUpToDatePins(t *testing.T) {
	plan, err := rootPlanner(t, map[string]string{"prettier": "patch"}, map[string]string{"prettier@3.9": "3.9.6"}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 {
		t.Errorf("changes = %v, want none for a pin already on its newest patch", plan.Changes)
	}
}

func TestPlanRejectsBadPolicies(t *testing.T) {
	cases := map[string]map[string]string{
		"cannot name go":      {"go": "pin"},
		"cannot name core:go": {"core:go": "minor"},
		"unknown policy":      {"node": "hold"},
		// No config anywhere in the repository names nodejs, so it is likely a typo for node.
		"mise.policies names nodejs, which no mise TOML config in the repository lists": {"nodejs": "minor"},
	}
	for want, policies := range cases {
		if _, err := rootPlanner(t, policies, nil).Plan(context.Background()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("policies %v: err = %v, want %q", policies, err, want)
		}
	}
}

func TestPlanWarnsOnPoliciesForToolsNotLoadedHere(t *testing.T) {
	// bun appears only in frontend/mise.toml, which mise does not load at the root, yet another
	// directory or MISE_ENV could load it.
	plan, err := rootPlanner(t, map[string]string{"bun": "pin"}, nil).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "mise.policies names bun, which appear only in configs mise does not load here") {
		t.Errorf("warnings = %q", plan.Warnings)
	}
}

func TestPlanLeavesAToolUncappedWhenAnyConfigCannotCapIt(t *testing.T) {
	files := map[string]string{
		"mise.toml":    "[tools]\nnode = \"26.8.2\"\n",
		"mise.ci.toml": "[tools]\nnode = \"26\"\n",
	}
	p, _ := planner(t, files, []string{"mise.toml", "mise.ci.toml"}, map[string]string{"node": "patch"}, map[string]string{"node@26.8": "26.8.5"})
	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.CapCommand() != nil || len(plan.Changes) != 0 {
		t.Errorf("CapCommand = %q, changes = %v; want neither while mise.ci.toml's node = 26 spans minors", plan.CapCommand(), plan.Changes)
	}
}

type failingRunner struct{}

func (failingRunner) Output(context.Context, string, []string, string, ...string) ([]byte, error) {
	return nil, fmt.Errorf("ran mise without policies")
}

func TestPlanWithoutPoliciesAsksMiseNothing(t *testing.T) {
	plan, err := Planner{Root: t.TempDir(), Run: failingRunner{}}.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "mise upgrade --bump --exclude go"; strings.Join(plan.UpgradeCommand(), " ") != want {
		t.Errorf("UpgradeCommand = %q, want %q", plan.UpgradeCommand(), want)
	}
	if plan.CapCommand() != nil {
		t.Errorf("CapCommand = %q, want nil", plan.CapCommand())
	}
}

func TestPlanReadsEveryConfigMiseLoadsAndAsksOncePerQuery(t *testing.T) {
	files := map[string]string{
		"mise.toml":    "[tools]\nnode = \"26.8.2\"\n",
		"mise.ci.toml": "[tools]\nnode = \"26.8.2\"\n",
	}
	// With MISE_ENV=ci, mise loads the environment config too.
	p, run := planner(t, files, []string{"mise.toml", "mise.ci.toml"}, map[string]string{"node": "minor"}, map[string]string{"node@26": "26.10.0"})
	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 2 || run.calls != 1 {
		t.Errorf("changes = %d, mise latest calls = %d; want 2 changes from 1 call", len(plan.Changes), run.calls)
	}
}
