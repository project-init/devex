// Package bufsync plans upgrades to the versions buf.gen.yaml pins: remote plugin versions and
// git input tags. A git input whose repository a go.mod requires as a module follows that
// module's version instead of a policy.
package bufsync

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"github.com/project-init/devex/internal/sre/dependencies/policy"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// Registry resolves the newest version of a remote plugin.
type Registry interface {
	LatestPlugin(ctx context.Context, plugin string) (string, error)
}

// Runner runs a command and returns its stdout.
type Runner interface {
	Output(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)
}

// Planner computes a Plan.
type Planner struct {
	// Root is the repository directory.
	Root string
	// Policies maps a remote plugin name or a git_repo URL to a policy name. Unlisted entries
	// use latest.
	Policies map[string]string
	Registry Registry
	Run      Runner
	// Timeout bounds each git tag listing. Zero leaves it unbounded.
	Timeout time.Duration
	// ListFiles returns the candidate files relative to Root. Nil uses pins.GitFiles.
	ListFiles func(root string) ([]string, error)
	// GoExclude lists the globs dependencies.go.exclude names. A go.mod they match holds no
	// module a git input follows.
	GoExclude []string
}

// Plan is every change a buf upgrade makes, resolved before any file is written.
type Plan struct {
	Changes []edit.Change
	// AllTemplates lists every buf.gen file, all regenerated when buf dep update changes a
	// buf.lock, since a template can generate from a module whose deps just moved.
	AllTemplates []string
	// DepModules lists the directories whose buf.yaml declares deps, for buf dep update.
	DepModules []string
	// Links lists the git inputs that follow a Go module, as go.mod stood when the plan was
	// made. Planner.Links resolves them again once the Go upgrade has run.
	Links    []Link
	Warnings []string
}

// Link is a git input tag that follows the version a go.mod requires of the same repository.
type Link struct {
	// Change moves the tag to the required version.
	Change edit.Change
	// Key is the input's git_repo, which buf.policies keys it by.
	Key string
	// Module is the Go module path, such as github.com/acme/protos.
	Module string
	// Required is the release go.mod builds against, the highest when several go.mod files
	// require the module directly.
	Required string
}

// On reports whether c rewrites the link's tag.
func (l Link) On(c edit.Change) bool {
	return l.Change.File == c.File && l.Change.Span == c.Span
}

// Moves reports whether the link rewrites its tag.
func (l Link) Moves() bool {
	return l.Change.To != l.Change.From
}

// With returns the plan's changes plus each link that moves, sorted by file and line, and the
// templates they touch. A link drops any planned change on its span, since an input go get -u
// linked after planning now follows go.mod, even where go.mod leaves the tag in place.
func (p Plan) With(links []Link) ([]edit.Change, []string) {
	changes := slices.DeleteFunc(slices.Clone(p.Changes), func(c edit.Change) bool {
		return slices.ContainsFunc(links, func(l Link) bool { return l.On(c) })
	})
	for _, link := range links {
		if link.Moves() {
			changes = append(changes, link.Change)
		}
	}
	slices.SortFunc(changes, func(a, b edit.Change) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	var templates []string
	for _, c := range changes {
		templates = append(templates, c.File)
	}

	return changes, slices.Compact(templates)
}

// Links returns every git input that follows a Go module, resolved against the tree as it
// stands, so a call after go get -u sees the versions it left. The warnings name inputs that
// match several modules, which link to none, and buf files it skipped as unparsable.
func (p Planner) Links() ([]Link, []string, error) {
	l, err := p.load(true)
	if err != nil {
		return nil, nil, err
	}

	return l.links(), l.linkWarnings, nil
}

// LinkedPolicy fails on a policy that names a linked input, which follows go.mod instead.
func LinkedPolicy(links []Link, policies map[string]string) error {
	for _, link := range links {
		if _, ok := policies[link.Key]; ok {
			return fmt.Errorf("buf.policies.%s: the input follows %s in go.mod, so it takes no policy", link.Key, link.Module)
		}
	}

	return nil
}

func (l loaded) links() []Link {
	var links []Link
	for _, pn := range l.found {
		if pn.module == "" {
			continue
		}
		required := l.requires[pn.module]
		change := edit.Change{File: pn.file, Line: pn.line, Span: pn.span, From: pn.version, To: tagFor(required)}
		links = append(links, Link{Change: change, Key: pn.key, Module: pn.module, Required: required})
	}

	return links
}

// GenerateCommand returns the directory and command that regenerate template's output.
func GenerateCommand(template string) (string, []string) {
	if base := path.Base(template); base != "buf.gen.yaml" {
		return path.Dir(template), []string{"buf", "generate", "--template", base}
	}

	return path.Dir(template), []string{"buf", "generate"}
}

// templateName matches buf.gen.yaml and its variants, such as buf.gen.swift.yaml.
var templateName = regexp.MustCompile(`^buf\.gen(\.[^/]+)?\.ya?ml$`)

// unlocatable explains a pin written in a form devex cannot find in its file, such as a block
// scalar.
const unlocatable = "is written in a form devex cannot locate, so devex leaves it alone"

// pin is one version buf.gen.yaml states.
type pin struct {
	file, key, version string
	line               int
	span               pins.Span
	// plugin reports a remote plugin, whose policy can only be latest or pin.
	plugin bool
	// unmanaged explains why devex leaves the pin alone, such as a plugin without a version.
	unmanaged string
	// module names the Go module a git input follows, or is empty.
	module string
}

// loaded is what every buf.gen template, buf.yaml, and go.mod under the root states.
type loaded struct {
	found        []pin
	requires     map[string]string
	templates    []string
	depModules   []string
	linkWarnings []string
}

func isGoMod(file string) bool {
	return path.Base(file) == "go.mod"
}

// load reads the tree. A lenient load, for Links, skips a buf file it cannot parse with a
// warning, so a stray template cannot fail a Go check; Plan fails on one.
func (p Planner) load(lenient bool) (loaded, error) {
	goExcluded, err := pins.Matcher(p.GoExclude)
	if err != nil {
		return loaded{}, fmt.Errorf("dependencies.go.exclude: %w", err)
	}
	files, err := pins.ReadFiles(p.Root, p.ListFiles, func(file string) bool {
		base := path.Base(file)

		return base == "buf.yaml" || templateName.MatchString(base) || isGoMod(file) && !goExcluded(file)
	})
	if err != nil {
		return loaded{}, err
	}

	var l loaded
	var goMods []pins.File
	for _, file := range files {
		if isGoMod(file.Path) {
			goMods = append(goMods, file)

			continue
		}
		top, err := parseTop(file)
		if err != nil && lenient {
			l.linkWarnings = append(l.linkWarnings, err.Error()+"; skipped")

			continue
		}
		if err != nil {
			return loaded{}, err
		}
		if path.Base(file.Path) == "buf.yaml" {
			if deps := pins.MappingValue(top, "deps"); deps != nil && len(deps.Content) > 0 {
				l.depModules = append(l.depModules, path.Dir(file.Path))
			}

			continue
		}
		l.templates = append(l.templates, file.Path)
		l.found = append(l.found, templatePins(file, top)...)
	}
	if l.requires, err = requirements(goMods); err != nil {
		return loaded{}, err
	}

	slices.SortStableFunc(l.found, func(a, b pin) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line))
	})
	for i, pn := range l.found {
		if pn.plugin || pn.unmanaged != "" {
			continue
		}
		modules := matchingModules(pn.key, l.requires)
		switch {
		case len(modules) > 1:
			l.linkWarnings = append(l.linkWarnings, fmt.Sprintf("%s:%d: %s matches Go modules %s; devex links it to none", pn.file, pn.line, pn.key, strings.Join(modules, ", ")))
		// A pseudo-version or a fork or local replace names no release to follow, so the input
		// keeps its policy.
		case len(modules) == 1 && tagFor(l.requires[modules[0]]) != "":
			l.found[i].module = modules[0]
		}
	}

	return l, nil
}

func parseTop(file pins.File) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(file.Data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file.Path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}

	return doc.Content[0], nil
}

// requirements returns the highest version the go.mod files build against for each module
// they require directly. Go builds against a replacement, so a replace naming another version of
// the same module sets the version, and a replace with a fork or a local path leaves "", which no
// tag follows.
func requirements(goMods []pins.File) (map[string]string, error) {
	requires := map[string]string{}
	for _, file := range goMods {
		f, err := modfile.Parse(file.Path, file.Data, nil)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file.Path, err)
		}
		// Like the go command, a replace naming the required version wins over one naming none.
		replaced := map[module.Version]string{}
		for _, r := range f.Replace {
			to := ""
			if r.New.Path == r.Old.Path {
				to = r.New.Version
			}
			replaced[r.Old] = to
		}
		for _, r := range f.Require {
			// An indirect requirement records a dependency's choice, so no tag follows it.
			if r.Indirect {
				continue
			}
			version := r.Mod.Version
			if to, ok := replaced[r.Mod]; ok {
				version = to
			} else if to, ok := replaced[module.Version{Path: r.Mod.Path}]; ok {
				version = to
			}
			if current, ok := requires[r.Mod.Path]; !ok || semver.Compare(version, current) > 0 {
				requires[r.Mod.Path] = version
			}
		}
	}

	return requires, nil
}

// matchingModules returns the required modules the git repository repo holds: its path, or
// that path with a major version suffix, such as github.com/acme/protos/v2.
func matchingModules(repo string, requires map[string]string) []string {
	base := modulePath(repo)
	if base == "" {
		return nil
	}

	var modules []string
	for m := range requires {
		if prefix, _, ok := module.SplitPathVersion(m); ok && prefix == base {
			modules = append(modules, m)
		}
	}
	slices.Sort(modules)

	return modules
}

// modulePath returns the Go module path a git repository URL names, such as
// github.com/acme/protos for https://github.com/acme/protos.git or git@github.com:acme/protos,
// or "" for a local path.
func modulePath(repo string) string {
	var host, repoPath string
	if strings.Contains(repo, "://") {
		u, err := url.Parse(repo)
		if err != nil {
			return ""
		}
		host, repoPath = u.Hostname(), u.Path
	} else {
		// An scp-like address, such as git@github.com:acme/protos.git.
		target := repo
		if i := strings.Index(repo, "@"); i >= 0 {
			target = repo[i+1:]
		}
		var ok bool
		if host, repoPath, ok = strings.Cut(target, ":"); !ok || strings.Contains(host, "/") {
			return ""
		}
	}
	repoPath = strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git")
	if host == "" || repoPath == "" {
		return ""
	}

	return strings.ToLower(host) + "/" + repoPath
}

// tagFor returns the tag that names the module version required: the version itself, since a
// Go module version is tagged exactly so. It returns "" for a pseudo-version, which names a
// commit rather than a tag.
func tagFor(required string) string {
	required = strings.TrimSuffix(required, "+incompatible")
	if !semver.IsValid(required) || module.IsPseudoVersion(required) {
		return ""
	}

	return required
}

// Plan reads every buf.gen template and buf.yaml, then resolves each pin its policy lets move.
// It writes nothing, so any error leaves the repository untouched.
func (p Planner) Plan(ctx context.Context) (Plan, error) {
	policies, err := policy.ParseAll("buf.policies", p.Policies)
	if err != nil {
		return Plan{}, err
	}
	l, err := p.load(false)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{AllTemplates: l.templates, DepModules: l.depModules, Links: l.links(), Warnings: l.linkWarnings}

	// Configuration errors, such as a typo in a policy key, fail before any lookup spends time on
	// the network.
	if err := LinkedPolicy(plan.Links, p.Policies); err != nil {
		return Plan{}, err
	}
	seen := map[string]bool{}
	for _, pn := range l.found {
		seen[pn.key] = true
		if pol, ok := policies[pn.key]; ok && pn.plugin && pol != policy.Latest && pol != policy.Pin {
			return Plan{}, fmt.Errorf("buf.policies.%s: remote plugins take latest or pin, since the registry reports only a plugin's newest version", pn.key)
		}
	}
	if unknown := policy.Unknown(policies, seen); len(unknown) > 0 {
		return Plan{}, fmt.Errorf("buf.policies names %s, which no buf.gen template references", strings.Join(unknown, ", "))
	}

	cache := lookups{plugins: map[string]string{}, tags: map[string][]string{}}
	for _, pn := range l.found {
		pol, ok := policies[pn.key]
		if !ok {
			pol = policy.Latest
		}
		if pol == policy.Pin {
			continue
		}
		if pn.unmanaged != "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s:%d: %s %s", pn.file, pn.line, pn.key, pn.unmanaged))

			continue
		}
		// Links resolves a linked input once go.mod settles.
		if pn.module != "" {
			continue
		}
		to, err := p.resolve(ctx, pn, pol, cache)
		if err != nil {
			return Plan{}, fmt.Errorf("%w; set buf.policies.%s to pin to skip the lookup", err, pn.key)
		}
		if to == "" || to == pn.version {
			continue
		}
		plan.Changes = append(plan.Changes, edit.Change{File: pn.file, Line: pn.line, Span: pn.span, From: pn.version, To: to})
	}

	return plan, nil
}

// lookups caches registry and git answers, since several templates can share a plugin or input.
type lookups struct {
	plugins map[string]string
	tags    map[string][]string
}

// resolve returns the version pol moves pn to, or "" when none is newer. It fails only when a
// lookup does.
func (p Planner) resolve(ctx context.Context, pn pin, pol policy.Policy, l lookups) (string, error) {
	if pn.plugin {
		latest, ok := l.plugins[pn.key]
		if !ok {
			var err error
			if latest, err = p.Registry.LatestPlugin(ctx, pn.key); err != nil {
				return "", err
			}
			l.plugins[pn.key] = latest
		}
		// A plugin version is an exact registry identifier, so it moves as published.
		if !policy.Newer(pn.version, latest) {
			return "", nil
		}

		return latest, nil
	}

	tags, ok := l.tags[pn.key]
	if !ok {
		var timeout error
		if p.Timeout > 0 {
			timeout = fmt.Errorf("timed out after %s: %w", p.Timeout, context.DeadlineExceeded)
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeoutCause(ctx, p.Timeout, timeout)
			defer cancel()
		}
		// A private repository without cached credentials fails instead of prompting.
		env := []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"}
		out, err := p.Run.Output(ctx, p.Root, env, "git", "ls-remote", "--tags", "--refs", "--end-of-options", pn.key)
		if err != nil && timeout != nil && context.Cause(ctx) == timeout {
			// A killed git reports only its signal, so the timeout says why it stopped.
			err = timeout
		}
		if err != nil {
			return "", fmt.Errorf("list tags of %s: %w", pn.key, err)
		}
		for line := range strings.Lines(string(out)) {
			if _, ref, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok {
				tags = append(tags, strings.TrimPrefix(ref, "refs/tags/"))
			}
		}
		l.tags[pn.key] = tags
	}

	return pol.Pick(pn.version, tags), nil
}

func templatePins(file pins.File, doc *yaml.Node) []pin {
	var found []pin
	offsets := pins.LineOffsets(file.Data)
	// setVersion manages pn at version, found offset bytes into node's value, or marks pn
	// unlocatable when the value sits off its line.
	setVersion := func(pn *pin, node *yaml.Node, offset int, version string) {
		start, ok := pins.LocateScalar(file.Data, offsets, node)
		if !ok {
			pn.unmanaged = unlocatable

			return
		}
		start += offset
		pn.version, pn.span = version, pins.Span{Start: start, End: start + len(version)}
	}

	for _, plugin := range items(pins.MappingValue(doc, "plugins")) {
		// v2 names a remote plugin under remote; v1 under plugin, where a local plugin has no path.
		remote := pins.MappingValue(plugin, "remote")
		if v1 := pins.MappingValue(plugin, "plugin"); remote == nil && v1 != nil && strings.Contains(v1.Value, "/") {
			remote = v1
		}
		if remote == nil {
			continue
		}
		name, version := splitRef(remote.Value)
		pn := pin{file: file.Path, key: name, line: remote.Line, plugin: true}
		switch {
		case version == "":
			pn.unmanaged = "has no version, so it floats to the newest plugin on every generate; pin one"
		case pins.MappingValue(plugin, "revision") != nil:
			// A revision belongs to one plugin version, so moving the version alone would break it.
			pn.unmanaged = "pins a revision, so devex leaves it alone"
		case policy.Components(version) == 0:
			pn.unmanaged = "is at " + version + ", which is not a release version, so devex leaves it alone"
		default:
			setVersion(&pn, remote, len(name)+1, version)
		}
		found = append(found, pn)
	}
	for _, input := range items(pins.MappingValue(doc, "inputs")) {
		if module := pins.MappingValue(input, "module"); module != nil {
			if name, version := splitRef(module.Value); version != "" {
				found = append(found, pin{file: file.Path, key: name, line: module.Line, unmanaged: "pins module input version " + version + ", which devex does not manage"})
			}
		}
		repo := pins.MappingValue(input, "git_repo")
		if repo == nil {
			continue
		}
		pn := pin{file: file.Path, key: repo.Value, line: repo.Line}
		switch tag := pins.MappingValue(input, "tag"); {
		case tag == nil:
			pn.unmanaged = "has no tag, so devex leaves it alone"
		case policy.Components(tag.Value) == 0:
			pn.line = tag.Line
			pn.unmanaged = "is at tag " + tag.Value + ", which is not a release version, so devex leaves it alone"
		default:
			pn.line = tag.Line
			setVersion(&pn, tag, 0, tag.Value)
		}
		found = append(found, pn)
	}

	return found
}

// splitRef splits a registry reference such as buf.build/acme/go:v1.2.0 into its name and
// version at the last colon, so a registry host with a port keeps its port.
func splitRef(ref string) (string, string) {
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i+1:], "/") {
		return ref, ""
	}

	return ref[:i], ref[i+1:]
}

// items returns a sequence's entries, or none for a missing key.
func items(n *yaml.Node) []*yaml.Node {
	if n == nil {
		return nil
	}

	return n.Content
}
