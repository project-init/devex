package gosync

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/version"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/project-init/devex/internal/sre/dependencies/goversion"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"golang.org/x/mod/modfile"
)

// Runner executes an external command in dir with the inherited environment, minus GOROOT,
// plus env.
type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) error
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
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// An empty GOROOT discards an inherited one, such as the root go run exports after switching
	// toolchains. Left in place, it pairs every go a child runs, mise's included, with another
	// release's compiler.
	cmd.Env = slices.Concat(os.Environ(), []string{"GOROOT="}, env)
	cmd.Stdout, cmd.Stderr = r.Stdout, &stderr
	if r.Stderr != nil {
		cmd.Stderr = io.MultiWriter(r.Stderr, &stderr)
	}
	if err := cmd.Run(); err != nil {
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

// Apply writes changes, replacing only each one's version digits, so comments, formatting,
// and tool options survive. It confirms every change still matches its planned text before
// writing anything, so a stale plan fails with the repository untouched.
func Apply(root string, changes []Change) error {
	byFile := map[string][]Change{}
	for _, c := range changes {
		byFile[c.File] = append(byFile[c.File], c)
	}

	type write struct {
		path string
		data []byte
	}
	var writes []write
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		abs := filepath.Join(root, filepath.FromSlash(file))
		data, err := rewriteSpans(abs, byFile[file])
		if err != nil {
			return err
		}
		writes = append(writes, write{abs, data})
	}
	for _, w := range writes {
		// WriteFile keeps an existing file's permissions.
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			return err
		}
	}

	return nil
}

// requiresNewerGo matches go's refusal to select a module version that needs a newer Go
// than the pinned toolchain.
var requiresNewerGo = regexp.MustCompile(`(?m)(\S+)@(\S+) requires go >= (\S+)`)

// Held is a module kept at its current version because its latest release needs a newer Go.
type Held struct {
	Module  string
	Version string
	Wanted  string
	NeedsGo string
}

// UpdateModules upgrades dependencies in every module under exactly the target toolchain.
// go get -u has no mode that skips updates needing a newer Go, so one such module would
// block every other upgrade. Instead, each is held at its current version and reported,
// while the rest upgrade. Holding a module back while its dependencies move can leave an
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
	var held []Held
	var holds []string
	seen := map[string]bool{}
	// Each round holds at least one module not held before, so the loop ends.
	for {
		err := run.Run(ctx, dir, env, "go", slices.Concat([]string{"get", "-u", "./..."}, holds)...)
		var cmdErr *CommandError
		if err == nil {
			return held, nil
		}
		if !errors.As(err, &cmdErr) {
			return nil, err
		}

		var blocked []Held
		for _, m := range requiresNewerGo.FindAllStringSubmatch(string(cmdErr.Stderr), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				blocked = append(blocked, Held{Module: m[1], Wanted: m[2], NeedsGo: m[3]})
			}
		}
		if len(blocked) == 0 {
			if len(held) > 0 {
				return nil, fmt.Errorf("go get -u in %s still fails after holding back %s: %w", dir, heldNames(held), err)
			}

			return nil, err
		}
		for _, h := range blocked {
			current, ok := requiredVersion(filepath.Join(dir, "go.mod"), h.Module)
			if !ok {
				return nil, fmt.Errorf("%s@%s needs Go %s, above target %s, and is not yet in go.mod to hold back: %w", h.Module, h.Wanted, h.NeedsGo, target, err)
			}
			h.Version = current
			held = append(held, h)
			holds = append(holds, h.Module+"@"+current)
		}
	}
}

func requiredVersion(goMod, module string) (string, bool) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", false
	}
	f, err := modfile.ParseLax(goMod, data, nil)
	if err != nil {
		return "", false
	}
	for _, r := range f.Require {
		if r.Mod.Path == module {
			return r.Mod.Version, true
		}
	}

	return "", false
}

// rewriteSpans returns file's contents with each change applied, or an error when any span no
// longer holds its planned text. It writes nothing.
func rewriteSpans(file string, changes []Change) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// Rewrite from the end so earlier offsets stay valid.
	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(b.Span.Start, a.Span.Start) })
	for _, c := range changes {
		if c.Span.End > len(data) || string(data[c.Span.Start:c.Span.End]) != c.From {
			return nil, fmt.Errorf("%s:%d changed since planning; expected %q there", file, c.Line, c.From)
		}
		data = slices.Replace(data, c.Span.Start, c.Span.End, []byte(c.To)...)
	}

	return data, nil
}
