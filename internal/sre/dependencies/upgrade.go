package dependencies

import (
	"context"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/project-init/devex/internal/sre/config"
	"github.com/project-init/devex/internal/sre/dependencies/gosync"
	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
)

// miseUpgrade bumps every mise tool except Go. Go moves only with --go, alongside every other
// Go pin.
var miseUpgrade = []string{"mise", "upgrade", "--bump", "--exclude", "go"}

// environment holds everything upgrade touches outside the process, so tests can fake it.
type environment struct {
	Root     string
	Run      gosync.Runner
	Resolver gosync.Resolver
	Registry gosync.Registry
	// HasMise reports whether mise is on PATH, so its shims may run the go command.
	HasMise bool
}

// runUpgrade plans the Go sync before anything runs, so a failed plan leaves the repository
// untouched. Go pins then move to the target, mise bumps every other tool, modules update
// under the target toolchain, and a final check confirms the invariant.
func runUpgrade(ctx context.Context, out io.Writer, o upgradeOptions, cfg config.DependenciesConfiguration, env environment) error {
	var settings gosync.Settings
	var plan gosync.Plan
	if o.goFlag {
		var err error
		if settings, err = gosync.NewSettings(env.Root, cfg.Go); err != nil {
			return err
		}
		planner := gosync.Planner{Settings: settings, Resolver: env.Resolver, Registry: env.Registry}
		if o.goVersion != "" {
			v, err := goversion.Parse(o.goVersion)
			if err != nil {
				return fmt.Errorf("--go-version: %w", err)
			}
			planner.Override = &v
		}
		if plan, err = planner.Plan(ctx); err != nil {
			return err
		}
		printWarnings(out, plan.Warnings)
	}

	// Without mise on PATH, no mise shim runs go, so nothing needs installing.
	var install []string
	if env.HasMise {
		install = gosync.MiseInstall(plan)
	}
	workspaces := slices.Sorted(maps.Keys(plan.Workspaces))
	if o.dryRun {
		printDryRun(out, o, plan, settings, install, workspaces)

		return nil
	}

	// Go pins are written before mise bumps anything else, since the bump can shift the text
	// of files devex edits in place, such as .tool-versions.
	if o.goFlag {
		_, _ = fmt.Fprintf(out, "Moving Go %s to %s...\n", currentLabel(plan), plan.Target)
		// Installing can fail, so it finishes before any file changes.
		if install != nil {
			if err := env.Run.Run(ctx, env.Root, nil, install[0], install[1:]...); err != nil {
				return err
			}
		}
		if err := gosync.Apply(env.Root, plan.Changes); err != nil {
			return err
		}
	}
	if o.miseFlag {
		_, _ = fmt.Fprintln(out, "Upgrading mise tools...")
		if err := env.Run.Run(ctx, env.Root, nil, miseUpgrade[0], miseUpgrade[1:]...); err != nil {
			return err
		}
	}
	if o.goFlag {
		_, _ = fmt.Fprintln(out, "Upgrading Go modules...")
		held, err := gosync.UpdateModules(ctx, env.Root, plan.Modules, workspaces, plan.Target, env.Run)
		if err != nil {
			return err
		}
		for _, h := range held {
			_, _ = fmt.Fprintf(out, "warning: held %s at %s; %s needs Go %s, above %s\n", h.Module, h.Version, h.Wanted, h.NeedsGo, plan.Target)
		}
		found, err := pins.Discover(settings.Discovery)
		if err != nil {
			return err
		}
		if _, err := gosync.Check(found, settings.Directive); err != nil {
			return fmt.Errorf("pins are out of step after the upgrade:\n%w", err)
		}
	}
	if o.bufFlag {
		_, _ = fmt.Fprintln(out, "Upgrading Buf dependencies...")
		if err := env.Run.Run(ctx, env.Root, nil, "buf", "dep", "update"); err != nil {
			return err
		}
	}

	return nil
}

func runCheck(out io.Writer, cfg config.DependenciesConfiguration, root string) error {
	settings, err := gosync.NewSettings(root, cfg.Go)
	if err != nil {
		return err
	}
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

	return nil
}

// printDryRun prints the plan in the order runUpgrade executes it.
func printDryRun(out io.Writer, o upgradeOptions, plan gosync.Plan, settings gosync.Settings, install, workspaces []string) {
	if o.goFlag {
		_, _ = fmt.Fprintf(out, "Go %s → %s (target: %s, directive: %s)\n", currentLabel(plan), plan.Target, settings.Target, settings.Directive)
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, c := range plan.Changes {
			_, _ = fmt.Fprintf(w, "  %s:%d\t%s\t%s → %s\n", c.File, c.Line, c.Kind, c.From, c.To)
		}
		_ = w.Flush()
		if install != nil {
			_, _ = fmt.Fprintln(out, strings.Join(install, " "))
		}
		if len(plan.Changes) == 0 {
			_, _ = fmt.Fprintln(out, "  every pin is already on target")
		}
	}
	if o.miseFlag {
		_, _ = fmt.Fprintln(out, strings.Join(miseUpgrade, " "))
	}
	if o.goFlag {
		moduleEnv := strings.Join(gosync.ModuleEnv(plan.Target), " ")
		for _, m := range plan.Modules {
			_, _ = fmt.Fprintf(out, "go get -u ./... && go mod tidy  (in %s, %s)\n", m, moduleEnv)
		}
		if len(plan.Modules) > 0 {
			_, _ = fmt.Fprintln(out, "  then go mod vendor in each vendored module outside a go.work directory, and "+strings.Join(gosync.HoldCheck, " ")+" in each module with holds")
		}
		for _, work := range workspaces {
			_, _ = fmt.Fprintf(out, "go work use  (in %s; a vendored workspace then runs go work vendor)\n", path.Dir(work))
		}
	}
	if o.bufFlag {
		_, _ = fmt.Fprintln(out, "buf dep update")
	}
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
