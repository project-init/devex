package gosync

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/version"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// Runner executes an external command in dir with the inherited environment, minus GOROOT,
// plus env.
type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) error
	// Output runs the command like Run and returns its stdout instead of streaming it.
	Output(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands as subprocesses, streaming their output.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

// CommandError is a failed command along with the stderr it wrote, so callers can react to
// the tool's diagnosis.
type CommandError struct {
	Command string
	Stderr  []byte
	Err     error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s: %v", e.Command, e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func (r ExecRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) error {
	return r.exec(ctx, dir, env, r.Stdout, name, args...)
}

func (r ExecRunner) Output(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	var stdout bytes.Buffer
	if err := r.exec(ctx, dir, env, &stdout, name, args...); err != nil {
		return nil, err
	}

	return stdout.Bytes(), nil
}

func (r ExecRunner) exec(ctx context.Context, dir string, env []string, stdout io.Writer, name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// A timed-out command's children, such as git's ssh, can hold its pipes open after it dies.
	if _, ok := ctx.Deadline(); ok {
		cmd.WaitDelay = time.Second
	}
	// An empty GOROOT discards an inherited one, such as the root go run exports after switching
	// toolchains. Left in place, it pairs every go a child runs, mise's included, with another
	// release's compiler.
	cmd.Env = slices.Concat(os.Environ(), []string{"GOROOT="}, env)
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if r.Stderr != nil {
		cmd.Stderr = io.MultiWriter(r.Stderr, &stderr)
	}
	// ErrWaitDelay means the command succeeded while a child it left behind, such as an ssh
	// ControlMaster, still held its pipes.
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return &CommandError{Command: fmt.Sprintf("%s %s (in %s)", name, strings.Join(args, " "), dir), Stderr: stderr.Bytes(), Err: err}
	}

	return nil
}

// toolchain returns the environment that pins every go command to exactly v, the way the
// golang images' GOTOOLCHAIN=local does: a dependency needing a newer Go fails loudly
// instead of silently raising the floor.
func toolchain(v goversion.Version) []string {
	return []string{"GOTOOLCHAIN=go" + v.Full()}
}

// ModuleEnv runs a go command against one module's own go.mod at exactly v. go mod tidy
// always ignores go.work, so GOWORK=off keeps go get and go build on the same graph.
func ModuleEnv(v goversion.Version) []string {
	return append(toolchain(v), "GOWORK=off")
}

// HoldCheck compiles a module with holds, test code included, to catch a hold that breaks
// the graph. -exec true links each test binary without running it, so no TestMain or vet
// finding can fail the check, and -mod=mod reads the module cache, so a vendor/ not yet
// refreshed cannot either.
var HoldCheck = []string{"go", "test", "-mod=mod", "-vet=off", "-exec", "true", "./..."}

// MiseInstall returns the command that installs the target Go, or nil when no pin mise reads,
// in a mise config or .tool-versions, moves. mise shims run go at the version the config names,
// so the version a pin moves to must be installed before the upgrade runs go. The command
// writes no config.
func MiseInstall(plan Plan) []string {
	for _, c := range plan.Changes {
		if c.Kind == pins.KindMise || c.Kind == pins.KindToolVersions {
			return []string{"mise", "install", "go@" + plan.Target.Full()}
		}
	}

	return nil
}

// requiresNewerGo matches go's refusal to select a module version that needs a newer Go
// than the pinned toolchain.
var requiresNewerGo = regexp.MustCompile(`(?m)(\S+)@(\S+) requires go >= (\S+)`)

// requiresHeld matches go get refusing a requested version because it needs a newer version of
// another requested module, directly or through others, such as
// "a@upgrade (v1.3.0) indirectly requires b@v1.2.0, not b@v1.1.0" or, when b's newer version
// is a prerelease or pseudo-version, "... not b@upgrade (v1.1.0)".
var requiresHeld = regexp.MustCompile(`(?m)(\S+)@(\S+)(?: \((\S+)\))? (?:indirectly )?requires (\S+)@(\S+), not \S+`)

// Held is a module kept at its current version because the version go get tried needs a newer Go
// or a newer release of another held module.
type Held struct {
	// Module is the held module's path.
	Module string
	// Version is the go.mod version Module stays at.
	Version string
	// Wanted is the version go get tried, which needs NeedsGo or Via at ViaVersion.
	Wanted string
	// NeedsGo is the Go version Module is held for or, when Via is set, the one Via is held for.
	NeedsGo string
	// Via names the held module that Wanted needs a newer release of.
	Via string
	// ViaVersion is the version of Via that Wanted requires.
	ViaVersion string
}

// UpdateModules upgrades each module's direct requirements with go get @upgrade under exactly the
// target toolchain, raising a requirement above @upgrade, such as to a prerelease, when another
// requirement's new version needs it. Indirect requirements move only as far as the direct
// requirements' new versions require; go get -u would take each to its newest release, which the
// modules importing it may not support yet. go get has no mode that skips updates needing a newer
// Go, so one such module would block every other upgrade. Instead, each is held at its current
// version and reported while the rest upgrade. A direct requirement whose new version needs a held
// module to move is held too. Holding a module back while its dependencies move can leave an
// incompatible graph, so a module with holds must still build, or the upgrade fails.
//
// workspaces lists each go.work, relative to root. go mod tidy can raise a module's go
// directive past its go.work's, which the go command refuses, so once every module has
// upgraded, go work use runs in each go.work.
func UpdateModules(ctx context.Context, root string, modules, workspaces []string, target goversion.Version, run Runner) ([]Held, error) {
	// GOWORK names each go.work outright, so an inherited GOWORK cannot redirect the edit. The
	// go command accepts only an absolute path there.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	env := ModuleEnv(target)

	var held []Held
	for _, dir := range modules {
		abs := filepath.Join(root, filepath.FromSlash(dir))
		moduleHeld, err := upgradeModule(ctx, abs, env, target, run)
		if err != nil {
			return nil, err
		}
		if err := run.Run(ctx, abs, env, "go", "mod", "tidy"); err != nil {
			return nil, err
		}
		// A vendored module builds from vendor/, which the upgrade leaves stale. A vendor/ beside
		// a go.work belongs to go work vendor, below.
		if !slices.Contains(workspaces, path.Join(dir, "go.work")) {
			if err := revendor(ctx, abs, env, run, "mod"); err != nil {
				return nil, err
			}
		}
		if len(moduleHeld) > 0 {
			if err := run.Run(ctx, abs, env, HoldCheck[0], HoldCheck[1:]...); err != nil && !noPackages(err) {
				return nil, fmt.Errorf("%s: the module does not compile with %s held back; if the holds caused it, wait for a Go release that supports them (they need Go %s) or pass --go-version: %w",
					dir, heldNames(moduleHeld), highestNeed(moduleHeld), err)
			}
		}
		held = append(held, moduleHeld...)
	}
	for _, work := range workspaces {
		file := filepath.Join(absRoot, filepath.FromSlash(work))
		dir := filepath.Dir(file)
		workEnv := append(toolchain(target), "GOWORK="+file)
		if err := run.Run(ctx, dir, workEnv, "go", "work", "use"); err != nil {
			return nil, err
		}
		if err := revendor(ctx, dir, workEnv, run, "work"); err != nil {
			return nil, err
		}
	}

	return held, nil
}

// revendor runs go <mode> vendor in dir when dir holds a vendor/ directory.
func revendor(ctx context.Context, dir string, env []string, run Runner, mode string) error {
	if _, err := os.Stat(filepath.Join(dir, "vendor", "modules.txt")); err != nil {
		return nil
	}

	return run.Run(ctx, dir, env, "go", mode, "vendor")
}

// noPackages reports whether err is go test finding no package that builds on this host,
// such as in a module whose files all carry another GOOS's build tag.
func noPackages(err error) bool {
	var cmdErr *CommandError

	return errors.As(err, &cmdErr) && strings.Contains(string(cmdErr.Stderr), "no packages to test")
}

func heldNames(held []Held) string {
	names := make([]string, len(held))
	for i, h := range held {
		names[i] = h.Module
	}

	return strings.Join(names, ", ")
}

// highestNeed returns the newest Go the held modules need, prereleases such as 1.27rc1
// included.
func highestNeed(held []Held) string {
	return slices.MaxFunc(held, func(a, b Held) int { return version.Compare("go"+a.NeedsGo, "go"+b.NeedsGo) }).NeedsGo
}

func upgradeModule(ctx context.Context, dir string, env []string, target goversion.Version, run Runner) ([]Held, error) {
	direct, versions, err := requirements(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	if len(direct) == 0 {
		return nil, nil
	}

	var held []Held
	// pinned indexes held by module.
	pinned := map[string]Held{}
	// minimums maps each direct requirement, by needer, to the version that needer's new version
	// requires above the requirement's own @upgrade, such as a prerelease. go get names the
	// requirement at the highest version an unpinned needer requires.
	minimums := map[string]map[string]string{}
	minimum := func(m string) string {
		highest := ""
		for needer, v := range minimums[m] {
			if _, ok := pinned[needer]; !ok && semver.Compare(v, highest) > 0 {
				highest = v
			}
		}

		return highest
	}
	// hold pins h at its go.mod version; cause is the go get failure that asked for it.
	hold := func(h Held, cause error) error {
		if _, ok := pinned[h.Module]; ok {
			return nil
		}
		current, ok := versions[h.Module]
		if !ok {
			return fmt.Errorf("%s@%s needs Go %s, above target %s, and is not yet in go.mod to hold back: %w", h.Module, h.Wanted, h.NeedsGo, target, cause)
		}
		h.Version = current
		pinned[h.Module] = h
		held = append(held, h)

		return nil
	}
	getArgs := func() []string {
		// @upgrade, unlike @latest, keeps a newer prerelease or pseudo-version in place.
		args := []string{"get"}
		for _, m := range direct {
			if _, ok := pinned[m]; !ok {
				args = append(args, m+"@"+cmp.Or(minimum(m), "upgrade"))
			}
		}
		for _, h := range held {
			args = append(args, h.Module+"@"+h.Version)
		}

		return args
	}
	// Holds only grow, and between holds each minimum only rises, so no round repeats another;
	// the loop gives up when a failure leaves the arguments unchanged.
	args := getArgs()
	for {
		err := run.Run(ctx, dir, env, "go", args...)
		var cmdErr *CommandError
		if err == nil {
			return held, nil
		}
		if !errors.As(err, &cmdErr) {
			return nil, err
		}

		for _, m := range requiresNewerGo.FindAllStringSubmatch(string(cmdErr.Stderr), -1) {
			if holdErr := hold(Held{Module: m[1], Wanted: m[2], NeedsGo: m[3]}, err); holdErr != nil {
				return nil, holdErr
			}
		}
		for _, m := range requiresHeld.FindAllStringSubmatch(string(cmdErr.Stderr), -1) {
			needer, wanted, needed, requires := m[1], cmp.Or(m[3], m[2]), m[4], m[5]
			if blocker, ok := pinned[needed]; ok {
				if holdErr := hold(Held{Module: needer, Wanted: wanted, NeedsGo: blocker.NeedsGo, Via: needed, ViaVersion: requires}, err); holdErr != nil {
					return nil, holdErr
				}
				continue
			}
			if _, ok := pinned[needer]; ok || !slices.Contains(direct, needed) {
				continue
			}
			if minimums[needed] == nil {
				minimums[needed] = map[string]string{}
			}
			if semver.Compare(requires, minimums[needed][needer]) > 0 {
				minimums[needed][needer] = requires
			}
		}
		next := getArgs()
		if slices.Equal(next, args) {
			if len(held) > 0 {
				return nil, fmt.Errorf("go get in %s still fails after holding back %s: %w", dir, heldNames(held), err)
			}

			return nil, err
		}
		args = next
	}
}

// requirements returns the modules goMod requires directly and the version of every module it
// requires. The direct list skips a module when a replace directive covers its go.mod version and
// either points at a local directory, which has no release to move to, or replaces only that exact
// version, which @upgrade would move off.
func requirements(goMod string) ([]string, map[string]string, error) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return nil, nil, err
	}
	f, err := modfile.Parse(goMod, data, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", goMod, err)
	}
	var mods []string
	versions := map[string]string{}
	for _, r := range f.Require {
		// go selects the highest of a module's repeated requirements.
		if semver.Compare(r.Mod.Version, versions[r.Mod.Path]) > 0 {
			versions[r.Mod.Path] = r.Mod.Version
		}
		if !r.Indirect && !slices.Contains(mods, r.Mod.Path) {
			mods = append(mods, r.Mod.Path)
		}
	}
	direct := slices.DeleteFunc(mods, func(mod string) bool {
		return slices.ContainsFunc(f.Replace, func(rep *modfile.Replace) bool {
			applies := rep.Old.Path == mod && (rep.Old.Version == "" || rep.Old.Version == versions[mod])
			return applies && (rep.New.Version == "" || rep.Old.Version != "")
		})
	})

	return direct, versions, nil
}
