// Package misesync plans a mise upgrade that honors a version policy per tool.
package misesync

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"github.com/project-init/devex/internal/sre/dependencies/policy"
)

// goTool is the key Go takes in mise. Go moves only with the Go sync, never with mise.
const goTool = "go"

// Runner runs a command and returns its stdout.
type Runner interface {
	Output(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)
}

// Planner computes a Plan.
type Planner struct {
	// Root is the repository directory, where mise runs.
	Root string
	// Policies maps tool keys to policy names. Unlisted tools use latest.
	Policies map[string]string
	Run      Runner
	// ListFiles returns the repository's files relative to Root, to find policy keys no loaded
	// config names. Nil uses pins.GitFiles.
	ListFiles func(root string) ([]string, error)
}

// Plan is every change a mise upgrade makes beyond what mise upgrade --bump does itself.
type Plan struct {
	// Changes rewrites the pins of tools capped at minor or patch.
	Changes []edit.Change
	// Exclude lists the tools mise upgrade --bump skips: Go and every tool whose policy is not
	// latest.
	Exclude []string
	// Capped lists the tools whose policy is minor or patch.
	Capped []string
	// Warnings lists entries a policy names but cannot cap.
	Warnings []string
}

// UpgradeCommand returns the mise command that bumps every tool the plan leaves to latest.
func (p Plan) UpgradeCommand() []string {
	cmd := []string{"mise", "upgrade", "--bump"}
	for _, tool := range p.Exclude {
		cmd = append(cmd, "--exclude", tool)
	}

	return cmd
}

// CapCommand returns the mise command that installs each capped tool within the range its
// config now names, or nil when no tool is capped. Without --bump, mise upgrade installs a
// rewritten pin and moves a prefix pin, such as node = "26", to its newest match, refreshing
// mise.lock for both.
func (p Plan) CapCommand() []string {
	if len(p.Capped) == 0 {
		return nil
	}

	return append([]string{"mise", "upgrade"}, p.Capped...)
}

// Plan reads each config mise loads at the repository root and resolves each capped tool with
// mise latest. It writes nothing, so any error leaves the repository untouched.
func (p Planner) Plan(ctx context.Context) (Plan, error) {
	for _, key := range slices.Sorted(maps.Keys(p.Policies)) {
		if pins.IsMiseGoKey(key) {
			return Plan{}, fmt.Errorf("mise.policies cannot name %s; go.target and go.directive govern Go", key)
		}
	}
	policies, err := policy.ParseAll("mise.policies", p.Policies)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Exclude: []string{goTool}}
	if len(policies) == 0 {
		return plan, nil
	}
	files, err := p.configs(ctx)
	if err != nil {
		return Plan{}, err
	}

	type candidate struct {
		tool pins.MiseTool
		pol  policy.Policy
	}
	exclude := map[string]bool{goTool: true}
	seen := map[string]bool{}
	uncappable := map[string]bool{}
	var candidates []candidate
	for _, file := range files {
		for _, tool := range pins.MiseTools(file.Path, file.Data) {
			seen[tool.Key] = true
			pol, ok := policies[tool.Key]
			if !ok || pol == policy.Latest {
				continue
			}
			exclude[tool.Key] = true
			if pol == policy.Pin {
				continue
			}
			// A pin with fewer components than the policy holds, such as node = "26" under patch,
			// spans more than the policy allows, so the tool stays pinned rather than drifting.
			switch n := policy.Components(tool.Version); {
			case n == 0:
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s:%d: %s = %q is not a release version, so its %s policy cannot cap it", tool.File, tool.Line, tool.Key, tool.Version, pol))
				uncappable[tool.Key] = true
			case n < pol.Fixed():
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s:%d: %s = %q spans more than its %s policy allows; write at least %d version components to cap it", tool.File, tool.Line, tool.Key, tool.Version, pol, pol.Fixed()))
				uncappable[tool.Key] = true
			default:
				candidates = append(candidates, candidate{tool, pol})
			}
		}
	}
	if err := p.checkUnknown(policies, seen, &plan); err != nil {
		return Plan{}, err
	}

	// A tool is capped only when every config that pins it can hold the cap, since mise upgrade
	// <tool> follows whichever config wins; otherwise none of its pins move.
	capped := map[string]bool{}
	latest := map[string]string{}
	for _, c := range candidates {
		if uncappable[c.tool.Key] {
			continue
		}
		capped[c.tool.Key] = true
		// A pin with exactly as many components as the policy holds, such as node = "26" under
		// minor, already names the range.
		if policy.Components(c.tool.Version) == c.pol.Fixed() {
			continue
		}
		change, err := p.cap(ctx, c.tool, c.pol, latest)
		if err != nil {
			return Plan{}, err
		}
		if change != nil {
			plan.Changes = append(plan.Changes, *change)
		}
	}
	plan.Exclude = slices.Sorted(maps.Keys(exclude))
	plan.Capped = slices.Sorted(maps.Keys(capped))

	return plan, nil
}

// checkUnknown fails on a policy key no tracked mise config names, which is likely a typo. A key
// named only by tracked configs mise does not load here, such as mise.ci.toml without
// MISE_ENV=ci, may govern the tool elsewhere, so it warns instead.
func (p Planner) checkUnknown(policies map[string]policy.Policy, seen map[string]bool, plan *Plan) error {
	unknown := policy.Unknown(policies, seen)
	if len(unknown) == 0 {
		return nil
	}
	files, err := pins.ReadFiles(p.Root, p.ListFiles, pins.IsMiseConfig)
	if err != nil {
		return err
	}
	tracked := map[string]bool{}
	for _, file := range files {
		for _, tool := range pins.MiseTools(file.Path, file.Data) {
			tracked[tool.Key] = true
		}
	}

	var missing, elsewhere []string
	for _, key := range unknown {
		if tracked[key] {
			elsewhere = append(elsewhere, key)
		} else {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("mise.policies names %s, which no mise TOML config in the repository lists; tools in .tool-versions take no policy", strings.Join(missing, ", "))
	}
	plan.Warnings = append(plan.Warnings, fmt.Sprintf("mise.policies names %s, which appear only in configs mise does not load here", strings.Join(elsewhere, ", ")))

	return nil
}

// cap resolves the newest release of tool that pol allows, caching mise latest by query.
func (p Planner) cap(ctx context.Context, tool pins.MiseTool, pol policy.Policy, latest map[string]string) (*edit.Change, error) {
	// Only the go backend matches a v-prefixed query; aqua and others print nothing for @v2.
	query := tool.Key + "@" + policy.Prefix(strings.TrimPrefix(tool.Version, "v"), pol.Fixed())
	newest, ok := latest[query]
	if !ok {
		out, err := p.Run.Output(ctx, p.Root, nil, "mise", "latest", query)
		if err != nil {
			return nil, err
		}
		if newest = strings.TrimSpace(string(out)); newest == "" {
			return nil, fmt.Errorf("mise latest %s found no release", query)
		}
		latest[query] = newest
	}
	// A newer patch leaves a two-component pin, such as 3.14 for 3.14.2, as written.
	to := policy.Format(tool.Version, newest)
	if !policy.Newer(tool.Version, newest) || to == tool.Version {
		return nil, nil
	}

	return &edit.Change{File: tool.File, Line: tool.Line, Span: tool.Span, From: tool.Version, To: to}, nil
}

// configs returns the repository's mise configs that mise loads in Root, asking mise itself so
// the set matches what mise upgrade acts on, MISE_ENV included. Global configs and those of
// parent directories fall outside the repository and are skipped.
func (p Planner) configs(ctx context.Context) ([]pins.File, error) {
	out, err := p.Run.Output(ctx, p.Root, nil, "mise", "config", "ls", "--json")
	if err != nil {
		return nil, err
	}
	var listed []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return nil, fmt.Errorf("read mise config ls: %w", err)
	}
	root, err := realPath(p.Root)
	if err != nil {
		return nil, err
	}

	var files []pins.File
	for _, l := range listed {
		// mise also lists a .tool-versions, which holds no TOML tool table.
		if filepath.Ext(l.Path) != ".toml" {
			continue
		}
		file, err := realPath(l.Path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, file)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		files = append(files, pins.File{Path: filepath.ToSlash(rel), Data: data})
	}

	return files, nil
}

// realPath resolves p to an absolute path without symlinks, so paths mise reports compare with
// the repository root even where a temporary directory sits behind a symlink.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}

	return filepath.EvalSymlinks(abs)
}
