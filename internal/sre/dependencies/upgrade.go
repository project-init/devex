package dependencies

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/bufsync"
	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/gosync"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/misesync"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

// bufDepUpdate refreshes buf.lock for a buf.yaml that declares deps.
var bufDepUpdate = []string{"buf", "dep", "update"}

// environment holds everything upgrade touches outside the process, so tests can fake it.
type environment struct {
	Root     string
	Run      gosync.Runner
	Resolver gosync.Resolver
	Registry gosync.Registry
	// Plugins resolves remote buf plugin versions.
	Plugins bufsync.Registry
	// HasMise reports whether mise is on PATH, so its shims may run the go command.
	HasMise bool
}

// plans holds every ecosystem's plan, all resolved before upgrade writes a file.
type plans struct {
	goSettings gosync.Settings
	goPlan     gosync.Plan
	goInstall  []string
	workspaces []string
	mise       misesync.Plan
	buf        bufsync.Plan
}

// runUpgrade plans every ecosystem before anything runs, so a failed plan leaves the repository
// untouched. Go pins then move to the target alongside capped mise pins, mise bumps every other
// tool, modules update under the target toolchain, a check confirms the Go invariant, and buf
// pins move last, so buf generate runs with any buf mise just bumped and git inputs that follow
// a Go module land where go get -u left it.
func runUpgrade(ctx context.Context, out io.Writer, o upgradeOptions, cfg config.DependenciesConfiguration, env environment) error {
	p, err := plan(ctx, out, o, cfg, env)
	if err != nil {
		return err
	}
	if o.dryRun {
		printDryRun(out, o, p)

		return nil
	}
	run := func(dir string, cmd []string) error {
		return env.Run.Run(ctx, filepath.Join(env.Root, filepath.FromSlash(dir)), nil, cmd[0], cmd[1:]...)
	}

	if o.goFlag {
		_, _ = fmt.Fprintf(out, "Moving Go %s to %s...\n", currentLabel(p.goPlan), p.goPlan.Target)
		// Installing can fail, so it finishes before any file changes.
		if p.goInstall != nil {
			if err := run(".", p.goInstall); err != nil {
				return err
			}
		}
	}
	// Go and capped mise pins can share a file, and both plans located their spans in its
	// original text, so they are written together before mise rewrites anything.
	if err := edit.Apply(env.Root, slices.Concat(p.goPlan.Changes, p.mise.Changes)); err != nil {
		return err
	}
	if o.miseFlag {
		_, _ = fmt.Fprintln(out, "Upgrading mise tools...")
		if err := run(".", p.mise.UpgradeCommand()); err != nil {
			return err
		}
		if capped := p.mise.CapCommand(); capped != nil {
			if err := run(".", capped); err != nil {
				return err
			}
		}
	}
	if o.goFlag {
		_, _ = fmt.Fprintln(out, "Upgrading Go modules...")
		held, err := gosync.UpdateModules(ctx, env.Root, p.goPlan.Modules, p.workspaces, p.goPlan.Target, env.Run)
		if err != nil {
			return err
		}
		for _, h := range held {
			_, _ = fmt.Fprintf(out, "warning: held %s at %s; %s needs Go %s, above %s\n", h.Module, h.Version, h.Wanted, h.NeedsGo, p.goPlan.Target)
		}
		found, err := pins.Discover(p.goSettings.Discovery)
		if err != nil {
			return err
		}
		if _, err := gosync.Check(found, p.goSettings.Directive); err != nil {
			return fmt.Errorf("pins are out of step after the upgrade:\n%w", err)
		}
		// Without --buf nothing moves a linked tag go get -u left behind, and check would fail.
		if !o.bufFlag {
			// The Go upgrade already succeeded, so a failed read here only warns.
			links, warnings, err := linkedInputs(env.Root, cfg, nil)
			if err != nil {
				warnings = append(warnings, "cannot check linked buf.gen tags: "+err.Error())
			}
			if err := bufsync.LinkedPolicy(links, cfg.Buf.Policies); err != nil {
				warnings = append(warnings, err.Error()+"; remove the policy before upgrade --buf")
			}
			for _, d := range drift(links) {
				warnings = append(warnings, d+"; run upgrade --buf to move it")
			}
			printWarnings(out, warnings)
		}
	}
	if o.bufFlag {
		_, _ = fmt.Fprintln(out, "Upgrading Buf dependencies...")
		links := p.buf.Links
		if o.goFlag {
			// Read after the Go upgrade, with a fresh listing, so each linked tag lands where go
			// get -u left its module.
			var warnings []string
			if links, warnings, err = linkedInputs(env.Root, cfg, nil); err != nil {
				return err
			}
			// Planning already printed the warnings that still hold.
			printWarnings(out, slices.DeleteFunc(warnings, func(w string) bool { return slices.Contains(p.buf.Warnings, w) }))
			// Planning skipped a linked input, so one go get -u unlinked stays put until a rerun.
			var unlinked []string
			for _, planned := range p.buf.Links {
				if !slices.ContainsFunc(links, func(l bufsync.Link) bool { return l.On(planned.Change) }) {
					c := planned.Change
					unlinked = append(unlinked, fmt.Sprintf("%s:%d: %s no longer follows a Go module after the upgrade; run upgrade --buf again to apply its policy", c.File, c.Line, planned.Key))
				}
			}
			printWarnings(out, unlinked)
		}
		changes, templates := p.buf.With(links)
		if err := edit.Apply(env.Root, changes); err != nil {
			return err
		}
		// Deps move first, so generation sees them. A moved buf.lock can change what any template
		// generates, so every template regenerates then.
		before := readLocks(env.Root, p.buf.DepModules)
		for _, dir := range p.buf.DepModules {
			if err := run(dir, bufDepUpdate); err != nil {
				return err
			}
		}
		if !maps.Equal(before, readLocks(env.Root, p.buf.DepModules)) {
			templates = p.buf.AllTemplates
		}
		for _, template := range templates {
			if err := run(bufsync.GenerateCommand(template)); err != nil {
				return err
			}
		}
	}

	return nil
}

// plan resolves every enabled ecosystem. It writes nothing.
func plan(ctx context.Context, out io.Writer, o upgradeOptions, cfg config.DependenciesConfiguration, env environment) (plans, error) {
	// Every planner reads the same working tree, so one listing serves them all.
	list := sharedListing(env.Root)

	var p plans
	var err error
	if o.goFlag {
		if p.goSettings, err = gosync.NewSettings(env.Root, cfg.Go); err != nil {
			return plans{}, err
		}
		// The check after the upgrade keeps its own listing, since the upgrade changes the tree.
		settings := p.goSettings
		settings.Discovery.ListFiles = list
		planner := gosync.Planner{Settings: settings, Resolver: env.Resolver, Registry: env.Registry}
		if o.goVersion != "" {
			v, err := goversion.Parse(o.goVersion)
			if err != nil {
				return plans{}, fmt.Errorf("--go-version: %w", err)
			}
			planner.Override = &v
		}
		if p.goPlan, err = planner.Plan(ctx); err != nil {
			return plans{}, err
		}
		printWarnings(out, p.goPlan.Warnings)
		// Without mise on PATH, no mise shim runs go, so nothing needs installing.
		if env.HasMise {
			p.goInstall = gosync.MiseInstall(p.goPlan)
		}
		p.workspaces = slices.Sorted(maps.Keys(p.goPlan.Workspaces))
	}
	if o.miseFlag {
		planner := misesync.Planner{Root: env.Root, Policies: cfg.Mise.Policies, Run: env.Run, ListFiles: list}
		if p.mise, err = planner.Plan(ctx); err != nil {
			return plans{}, err
		}
		printWarnings(out, p.mise.Warnings)
	}
	if o.bufFlag {
		planner := bufsync.Planner{Root: env.Root, Policies: cfg.Buf.Policies, Registry: env.Plugins, Run: env.Run, Timeout: networkTimeout, ListFiles: list, GoExclude: cfg.Go.Exclude}
		if p.buf, err = planner.Plan(ctx); err != nil {
			return plans{}, err
		}
		printWarnings(out, p.buf.Warnings)
	}

	return p, nil
}

func runCheck(out io.Writer, cfg config.DependenciesConfiguration, root string) error {
	settings, err := gosync.NewSettings(root, cfg.Go)
	if err != nil {
		return err
	}
	// Go discovery and the buf drift check read the same tree, so one listing serves both.
	settings.Discovery.ListFiles = sharedListing(root)
	found, err := pins.Discover(settings.Discovery)
	if err != nil {
		return err
	}
	printWarnings(out, append(found.Warnings, pins.ShadowWarnings(root, found)...))
	current, err := gosync.Check(found, settings.Directive)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Go %s: %d pins in step (directive: %s)\n", current, len(found.Pins), settings.Directive)
	links, warnings, err := linkedInputs(root, cfg, settings.Discovery.ListFiles)
	if err != nil {
		return err
	}
	printWarnings(out, warnings)
	if err := bufsync.LinkedPolicy(links, cfg.Buf.Policies); err != nil {
		return err
	}
	if lines := drift(links); len(lines) > 0 {
		return fmt.Errorf("buf.gen git inputs are out of step with go.mod:\n  %s", strings.Join(lines, "\n  "))
	}

	return nil
}

// linkedInputs returns the buf.gen git inputs that follow a Go module and the link warnings. A
// nil list uses pins.GitFiles.
func linkedInputs(root string, cfg config.DependenciesConfiguration, list func(string) ([]string, error)) ([]bufsync.Link, []string, error) {
	return bufsync.Planner{Root: root, ListFiles: list, GoExclude: cfg.Go.Exclude}.Links()
}

// drift describes each link whose tag differs from the version go.mod builds against.
func drift(links []bufsync.Link) []string {
	var lines []string
	for _, link := range links {
		if c := link.Change; link.Moves() {
			lines = append(lines, fmt.Sprintf("%s:%d: tag %s, but go.mod builds against %s %s", c.File, c.Line, c.From, link.Module, link.Required))
		}
	}

	return lines
}

// sharedListing lists root's files once, however many readers ask.
func sharedListing(root string) func(string) ([]string, error) {
	files := sync.OnceValues(func() ([]string, error) { return pins.GitFiles(root) })

	return func(string) ([]string, error) { return files() }
}

// printDryRun prints the plan in the order runUpgrade executes it.
func printDryRun(out io.Writer, o upgradeOptions, p plans) {
	if o.goFlag {
		_, _ = fmt.Fprintf(out, "Go %s → %s (target: %s, directive: %s)\n", currentLabel(p.goPlan), p.goPlan.Target, p.goSettings.Target, p.goSettings.Directive)
		printEdits(out, p.goPlan.Changes)
		if p.goInstall != nil {
			_, _ = fmt.Fprintln(out, strings.Join(p.goInstall, " "))
		}
		if len(p.goPlan.Changes) == 0 {
			_, _ = fmt.Fprintln(out, "  every pin is already on target")
		}
	}
	if o.miseFlag {
		printEdits(out, p.mise.Changes)
		_, _ = fmt.Fprintln(out, strings.Join(p.mise.UpgradeCommand(), " "))
		if capped := p.mise.CapCommand(); capped != nil {
			_, _ = fmt.Fprintln(out, strings.Join(capped, " "))
		}
	}
	if o.goFlag {
		moduleEnv := strings.Join(gosync.ModuleEnv(p.goPlan.Target), " ")
		for _, m := range p.goPlan.Modules {
			_, _ = fmt.Fprintf(out, "go get -u ./... && go mod tidy  (in %s, %s)\n", m, moduleEnv)
		}
		if len(p.goPlan.Modules) > 0 {
			_, _ = fmt.Fprintln(out, "  then go mod vendor in each vendored module outside a go.work directory, and "+strings.Join(gosync.HoldCheck, " ")+" in each module with holds")
		}
		for _, work := range p.workspaces {
			_, _ = fmt.Fprintf(out, "go work use  (in %s; a vendored workspace then runs go work vendor)\n", path.Dir(work))
		}
	}
	if o.bufFlag {
		// Without --go, go.mod stays put, so the links resolve now; with it, they wait for the Go
		// upgrade.
		var links, following []bufsync.Link
		if o.goFlag {
			following = p.buf.Links
		} else {
			links = p.buf.Links
		}
		changes, templates := p.buf.With(links)
		printEdits(out, changes)
		for _, link := range following {
			c := link.Change
			_, _ = fmt.Fprintf(out, "  %s:%d  %s follows %s in go.mod after the Go upgrade, now %s\n", c.File, c.Line, c.From, link.Module, link.Required)
		}
		for _, dir := range p.buf.DepModules {
			_, _ = fmt.Fprintf(out, "%s  (in %s)\n", strings.Join(bufDepUpdate, " "), dir)
		}
		for _, template := range templates {
			dir, cmd := bufsync.GenerateCommand(template)
			_, _ = fmt.Fprintf(out, "%s  (in %s)\n", strings.Join(cmd, " "), dir)
		}
		if len(following) > 0 {
			_, _ = fmt.Fprintln(out, "  then buf generate beside each template whose linked tag moves")
		}
		if len(p.buf.DepModules) > 0 {
			_, _ = fmt.Fprintln(out, "  then buf generate beside every other template if a buf.lock changes")
		}
	}
}

// readLocks returns the buf.lock beside each module directory, keyed by directory; a missing
// lock reads as empty.
func readLocks(root string, dirs []string) map[string]string {
	locks := make(map[string]string, len(dirs))
	for _, dir := range dirs {
		data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "buf.lock"))
		locks[dir] = string(data)
	}

	return locks
}

// printEdits lists changes as a table, with a kind column for Go pins.
func printEdits(out io.Writer, changes []edit.Change) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, c := range changes {
		kind := ""
		if c.Kind != "" {
			kind = string(c.Kind) + "\t"
		}
		_, _ = fmt.Fprintf(w, "  %s:%d\t%s%s → %s\n", c.File, c.Line, kind, c.From, c.To)
	}
	_ = w.Flush()
}

func printWarnings(out io.Writer, warnings []string) {
	for _, w := range warnings {
		_, _ = fmt.Fprintln(out, "warning: "+w)
	}
}

// currentLabel describes the starting version, which an override may leave undecided.
func currentLabel(plan gosync.Plan) string {
	if plan.Current.Precision == 0 {
		return "(pins disagree)"
	}

	return plan.Current.String()
}
