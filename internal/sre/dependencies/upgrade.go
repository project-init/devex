package dependencies

import (
	"context"
	"errors"
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
	"github.com/project-init/devex/internal/ui"
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
// a Go module land where the Go upgrade left it.
func runUpgrade(ctx context.Context, u *ui.UI, o upgradeOptions, cfg config.DependenciesConfiguration, env environment) error {
	var p plans
	err := u.Spin(ctx, "Planning the upgrade", func(ctx context.Context) error {
		var err error
		p, err = plan(ctx, o, cfg, env)
		return err
	})
	// Warnings print once the spinner stops, including those of the ecosystems that planned
	// before another failed.
	printWarnings(u, p.warnings())
	if err != nil {
		return err
	}
	if o.dryRun {
		if u.JSON() {
			return u.WriteJSON(planDocument(o, p))
		}
		printDryRun(u.Out(), o, p)

		return nil
	}
	run := func(dir string, cmd []string) error {
		return env.Run.Run(ctx, filepath.Join(env.Root, filepath.FromSlash(dir)), nil, cmd[0], cmd[1:]...)
	}

	if o.goFlag {
		u.Status("Moving Go %s to %s...", currentLabel(p.goPlan), p.goPlan.Target)
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
		u.Status("Upgrading mise tools...")
		// Install the rewritten pins before mise resolves anything else: an uncapped tool can
		// depend on a capped one, as an npm tool does on node, and mise upgrade --bump fails while
		// that dependency's new version is uninstalled.
		for _, cmd := range p.mise.Commands() {
			if err := run(".", cmd); err != nil {
				return err
			}
		}
	}
	if o.goFlag {
		u.Status("Upgrading Go modules...")
		held, err := gosync.UpdateModules(ctx, env.Root, p.goPlan.Modules, p.workspaces, p.goPlan.Target, env.Run)
		if err != nil {
			return err
		}
		var holds []string
		for _, h := range held {
			holds = append(holds, heldWarning(h, p.goPlan.Target))
		}
		printWarnings(u, holds)
		found, err := pins.Discover(p.goSettings.Discovery)
		if err != nil {
			return err
		}
		if _, err := gosync.Check(found, p.goSettings.Directive); err != nil {
			return fmt.Errorf("pins are out of step after the upgrade:\n%w", err)
		}
		// Without --buf nothing moves a linked tag the Go upgrade left behind, and check would fail.
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
			printWarnings(u, warnings)
		}
	}
	if o.bufFlag {
		u.Status("Upgrading Buf dependencies...")
		links := p.buf.Links
		if o.goFlag {
			// Read after the Go upgrade, with a fresh listing, so each linked tag lands where the
			// upgrade left its module.
			var warnings []string
			if links, warnings, err = linkedInputs(env.Root, cfg, nil); err != nil {
				return err
			}
			// Planning already printed the warnings that still hold.
			printWarnings(u, slices.DeleteFunc(warnings, func(w string) bool { return slices.Contains(p.buf.Warnings, w) }))
			// Planning skipped a linked input, so an input the Go upgrade unlinks stays put until a rerun.
			var unlinked []string
			for _, planned := range p.buf.Links {
				if !slices.ContainsFunc(links, func(l bufsync.Link) bool { return l.On(planned.Change) }) {
					c := planned.Change
					unlinked = append(unlinked, fmt.Sprintf("%s:%d: %s no longer follows a Go module after the upgrade; run upgrade --buf again to apply its policy", c.File, c.Line, planned.Key))
				}
			}
			printWarnings(u, unlinked)
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

// plan resolves every enabled ecosystem concurrently. It writes nothing. It returns the first
// failure in a fixed order, go, mise, then buf, whichever finishes first.
func plan(ctx context.Context, o upgradeOptions, cfg config.DependenciesConfiguration, env environment) (plans, error) {
	// Every planner reads the same working tree, so one listing serves them all.
	list := sharedListing(env.Root)

	var p plans
	var goErr, miseErr, bufErr error
	var wg sync.WaitGroup
	if o.goFlag {
		wg.Go(func() { goErr = p.planGo(ctx, o, cfg, env, list) })
	}
	if o.miseFlag {
		wg.Go(func() {
			if miseErr = misesync.CheckVersion(ctx, env.Run, env.Root); miseErr != nil {
				return
			}
			planner := misesync.Planner{Root: env.Root, Policies: cfg.Mise.Policies, Run: env.Run, ListFiles: list}
			p.mise, miseErr = planner.Plan(ctx)
		})
	}
	if o.bufFlag {
		wg.Go(func() {
			planner := bufsync.Planner{Root: env.Root, Policies: cfg.Buf.Policies, Registry: env.Plugins, Run: env.Run, Timeout: networkTimeout, ListFiles: list, GoExclude: cfg.Go.Exclude}
			p.buf, bufErr = planner.Plan(ctx)
		})
	}
	wg.Wait()

	for _, err := range []error{goErr, miseErr, bufErr} {
		if err != nil {
			return p, err // with the warnings the other ecosystems found
		}
	}

	return p, nil
}

func (p *plans) planGo(ctx context.Context, o upgradeOptions, cfg config.DependenciesConfiguration, env environment, list func(string) ([]string, error)) error {
	var err error
	if p.goSettings, err = gosync.NewSettings(env.Root, cfg.Go); err != nil {
		return err
	}
	// The check after the upgrade keeps its own listing, since the upgrade changes the tree.
	settings := p.goSettings
	settings.Discovery.ListFiles = list
	planner := gosync.Planner{Settings: settings, Resolver: env.Resolver, Registry: env.Registry}
	if o.goVersion != "" {
		v, err := goversion.Parse(o.goVersion)
		if err != nil {
			return fmt.Errorf("--go-version: %w", err)
		}
		planner.Override = &v
	}
	if p.goPlan, err = planner.Plan(ctx); err != nil {
		return err
	}
	// Without mise on PATH, no mise shim runs go, so nothing needs installing.
	if env.HasMise {
		p.goInstall = gosync.MiseInstall(p.goPlan)
	}
	p.workspaces = slices.Sorted(maps.Keys(p.goPlan.Workspaces))

	return nil
}

// warnings are every ecosystem's planning warnings, go first, then mise, then buf.
func (p *plans) warnings() []string {
	return slices.Concat(p.goPlan.Warnings, p.mise.Warnings, p.buf.Warnings)
}

func runCheck(u *ui.UI, cfg config.DependenciesConfiguration, root string) error {
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
	report := checkReport{Warnings: append(found.Warnings, pins.ShadowWarnings(root, found)...)}

	// Every check runs, so one pass reports every problem.
	var errs []error
	current, goErr := gosync.Check(found, settings.Directive)
	if goErr != nil {
		errs = append(errs, goErr)
		report.Problems = append(report.Problems, goProblems(goErr)...)
	} else {
		report.Go = &checkGo{Version: current.String(), Pins: len(found.Pins), Directive: settings.Directive}
	}
	links, warnings, err := linkedInputs(root, cfg, settings.Discovery.ListFiles)
	if err != nil {
		// The Go checks still stand, so the failure joins them as one more problem.
		errs = append(errs, err)
		report.Problems = append(report.Problems, checkProblem{Check: "read buf.gen inputs", Reason: err.Error()})
	}
	report.Warnings = append(report.Warnings, warnings...)
	var driftLines []string
	for _, link := range links {
		c := link.Change
		if err := link.PolicyError(cfg.Buf.Policies); err != nil {
			errs = append(errs, err)
			report.Problems = append(report.Problems, checkProblem{Check: "linked buf.gen input takes a policy", File: c.File, Line: c.Line, Reason: err.Error()})
		}
		if reason := link.Drift(); reason != "" {
			driftLines = append(driftLines, driftLine(link))
			report.Problems = append(report.Problems, checkProblem{Check: "buf.gen git input out of step with go.mod", File: c.File, Line: c.Line, Version: c.From, Reason: reason})
		}
	}
	if len(driftLines) > 0 {
		errs = append(errs, fmt.Errorf("buf.gen git inputs are out of step with go.mod:\n  %s", strings.Join(driftLines, "\n  ")))
	}

	printWarnings(u, report.Warnings)
	if u.JSON() {
		report.Problems = orEmpty(report.Problems)
		report.Warnings = orEmpty(report.Warnings)
		if err := u.WriteJSON(report); err != nil {
			return err
		}
		if len(report.Problems) > 0 {
			return fmt.Errorf("found %d dependency problems", len(report.Problems))
		}
		return nil
	}
	if report.Go != nil {
		u.Printf("Go %s: %d pins in step (directive: %s)\n", report.Go.Version, report.Go.Pins, report.Go.Directive)
	}

	return errors.Join(errs...)
}

// checkReport is check's --output json document.
type checkReport struct {
	Go       *checkGo       `json:"go,omitempty"` // absent when the toolchain pins disagree
	Problems []checkProblem `json:"problems"`
	Warnings []string       `json:"warnings"`
}

type checkGo struct {
	Version   string `json:"version"`
	Pins      int    `json:"pins"`
	Directive string `json:"directive"`
}

// checkProblem is one pin or input that fails a check.
type checkProblem struct {
	Check   string `json:"check"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// goProblems flattens gosync.Check's error into one problem per pin.
func goProblems(err error) []checkProblem {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var problems []checkProblem
		for _, e := range joined.Unwrap() {
			problems = append(problems, goProblems(e)...)
		}
		return problems
	}
	var pe *gosync.ProblemsError
	if !errors.As(err, &pe) {
		return []checkProblem{{Check: err.Error()}}
	}
	problems := make([]checkProblem, len(pe.Problems))
	for i, p := range pe.Problems {
		problems[i] = checkProblem{Check: pe.Title, File: p.Pin.File, Line: p.Pin.Line, Kind: string(p.Pin.Kind), Version: p.Pin.Version.String(), Reason: p.Reason}
	}
	return problems
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
		if link.Drift() != "" {
			lines = append(lines, driftLine(link))
		}
	}

	return lines
}

func driftLine(link bufsync.Link) string {
	return fmt.Sprintf("%s:%d: %s", link.Change.File, link.Change.Line, link.Drift())
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
		for _, cmd := range p.mise.Commands() {
			_, _ = fmt.Fprintln(out, strings.Join(cmd, " "))
		}
	}
	if o.goFlag {
		moduleEnv := strings.Join(gosync.ModuleEnv(p.goPlan.Target), " ")
		for _, m := range p.goPlan.Modules {
			_, _ = fmt.Fprintf(out, "go get <each direct requirement>@upgrade && go mod tidy  (in %s, %s)\n", m, moduleEnv)
		}
		if len(p.goPlan.Modules) > 0 {
			_, _ = fmt.Fprintln(out, "  then go mod vendor in each vendored module outside a go.work directory, and "+strings.Join(gosync.HoldCheck, " ")+" in each module with holds")
		}
		for _, work := range p.workspaces {
			_, _ = fmt.Fprintf(out, "go work use  (in %s; a vendored workspace then runs go work vendor)\n", path.Dir(work))
		}
	}
	if o.bufFlag {
		links, following := o.splitLinks(p)
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

// splitLinks divides the buf inputs that follow a Go module into those that resolve now and those
// that wait for the Go upgrade. Without --go, go.mod stays put, so every link resolves now.
func (o upgradeOptions) splitLinks(p plans) (now, following []bufsync.Link) {
	if o.goFlag {
		return nil, p.buf.Links
	}

	return p.buf.Links, nil
}

// planDocument is the upgrade plan as --output json prints it, in the order runUpgrade
// executes it.
func planDocument(o upgradeOptions, p plans) planJSON {
	doc := planJSON{Warnings: orEmpty(p.warnings())}
	if o.goFlag {
		doc.Go = &goPlanJSON{
			Target:     p.goPlan.Target.String(),
			Changes:    changesJSON(p.goPlan.Changes),
			Install:    p.goInstall,
			Modules:    orEmpty(p.goPlan.Modules),
			Workspaces: orEmpty(p.workspaces),
		}
		if p.goPlan.Current.Precision != 0 {
			doc.Go.Current = p.goPlan.Current.String()
		}
	}
	if o.miseFlag {
		doc.Mise = &misePlanJSON{Changes: changesJSON(p.mise.Changes), Commands: p.mise.Commands()}
	}
	if o.bufFlag {
		links, waiting := o.splitLinks(p)
		following := []followJSON{}
		for _, link := range waiting {
			following = append(following, followJSON{File: link.Change.File, Line: link.Change.Line, From: link.Change.From, Module: link.Module, Required: link.Required})
		}
		changes, templates := p.buf.With(links)
		doc.Buf = &bufPlanJSON{Changes: changesJSON(changes), Following: following,
			DepModules: orEmpty(p.buf.DepModules), Templates: orEmpty(templates)}
	}
	return doc
}

type planJSON struct {
	Go       *goPlanJSON   `json:"go,omitempty"`
	Mise     *misePlanJSON `json:"mise,omitempty"`
	Buf      *bufPlanJSON  `json:"buf,omitempty"`
	Warnings []string      `json:"warnings"`
}

type goPlanJSON struct {
	Current    string       `json:"current,omitempty"` // absent when the pins disagree
	Target     string       `json:"target"`
	Changes    []changeJSON `json:"changes"`
	Install    []string     `json:"install,omitempty"`
	Modules    []string     `json:"modules"`
	Workspaces []string     `json:"workspaces"`
}

type misePlanJSON struct {
	Changes  []changeJSON `json:"changes"`
	Commands [][]string   `json:"commands"`
}

type bufPlanJSON struct {
	Changes    []changeJSON `json:"changes"`
	Following  []followJSON `json:"following"` // linked inputs that move with the Go upgrade
	DepModules []string     `json:"depModules"`
	Templates  []string     `json:"templates"` // regenerated beside each template
}

type changeJSON struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Kind string `json:"kind,omitempty"`
	From string `json:"from"`
	To   string `json:"to"`
}

type followJSON struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	From     string `json:"from"`
	Module   string `json:"module"`
	Required string `json:"required"`
}

// orEmpty keeps JSON printing [] rather than null for an empty list.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func changesJSON(changes []edit.Change) []changeJSON {
	out := make([]changeJSON, len(changes))
	for i, c := range changes {
		out[i] = changeJSON{File: c.File, Line: c.Line, Kind: string(c.Kind), From: c.From, To: c.To}
	}
	return out
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

func printWarnings(u *ui.UI, warnings []string) {
	for _, w := range warnings {
		u.Warn("%s", w)
	}
}

// currentLabel describes the starting version, which an override may leave undecided.
func currentLabel(plan gosync.Plan) string {
	if plan.Current.Precision == 0 {
		return "(pins disagree)"
	}

	return plan.Current.String()
}

func heldWarning(h gosync.Held, target goversion.Version) string {
	if h.Via != "" {
		return fmt.Sprintf("held %s at %s; %s needs %s %s, and %s is held for Go %s, above %s", h.Module, h.Version, h.Wanted, h.Via, h.ViaVersion, h.Via, h.NeedsGo, target)
	}

	return fmt.Sprintf("held %s at %s; %s needs Go %s, above %s", h.Module, h.Version, h.Wanted, h.NeedsGo, target)
}
