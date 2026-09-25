// Package bufsync plans upgrades to the versions buf.gen.yaml pins: remote plugin versions and
// git input tags.
package bufsync

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/dependencies/edit"
	"github.com/project-init/devex/internal/sre/dependencies/pins"
	"github.com/project-init/devex/internal/sre/dependencies/policy"
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
}

// Plan is every change a buf upgrade makes, resolved before any file is written.
type Plan struct {
	Changes []edit.Change
	// Templates lists the buf.gen files the plan changes, relative to Root, each regenerated
	// after the write.
	Templates []string
	// AllTemplates lists every buf.gen file, all regenerated when buf dep update changes a
	// buf.lock, since a template can generate from a module whose deps just moved.
	AllTemplates []string
	// DepModules lists the directories whose buf.yaml declares deps, for buf dep update.
	DepModules []string
	Warnings   []string
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
}

// Plan reads every buf.gen template and buf.yaml, then resolves each pin its policy lets move.
// It writes nothing, so any error leaves the repository untouched.
func (p Planner) Plan(ctx context.Context) (Plan, error) {
	policies, err := policy.ParseAll("buf.policies", p.Policies)
	if err != nil {
		return Plan{}, err
	}
	files, err := pins.ReadFiles(p.Root, p.ListFiles, func(file string) bool {
		base := path.Base(file)

		return base == "buf.yaml" || templateName.MatchString(base)
	})
	if err != nil {
		return Plan{}, err
	}

	var plan Plan
	var found []pin
	for _, file := range files {
		var doc yaml.Node
		if err := yaml.Unmarshal(file.Data, &doc); err != nil {
			return Plan{}, fmt.Errorf("parse %s: %w", file.Path, err)
		}
		var top *yaml.Node
		if len(doc.Content) > 0 {
			top = doc.Content[0]
		}
		if path.Base(file.Path) == "buf.yaml" {
			if deps := pins.MappingValue(top, "deps"); deps != nil && len(deps.Content) > 0 {
				plan.DepModules = append(plan.DepModules, path.Dir(file.Path))
			}

			continue
		}
		plan.AllTemplates = append(plan.AllTemplates, file.Path)
		found = append(found, templatePins(file, top)...)
	}

	slices.SortStableFunc(found, func(a, b pin) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line))
	})
	// Configuration errors, such as a typo in a policy key, fail before any lookup spends time on
	// the network.
	seen := map[string]bool{}
	for _, pn := range found {
		seen[pn.key] = true
		if pol, ok := policies[pn.key]; ok && pn.plugin && pol != policy.Latest && pol != policy.Pin {
			return Plan{}, fmt.Errorf("buf.policies.%s: remote plugins take latest or pin, since the registry reports only a plugin's newest version", pn.key)
		}
	}
	if unknown := policy.Unknown(policies, seen); len(unknown) > 0 {
		return Plan{}, fmt.Errorf("buf.policies names %s, which no buf.gen template references", strings.Join(unknown, ", "))
	}

	changed := map[string]bool{}
	l := lookups{plugins: map[string]string{}, tags: map[string][]string{}}
	for _, pn := range found {
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
		to, err := p.resolve(ctx, pn, pol, l)
		if err != nil {
			return Plan{}, fmt.Errorf("%w; set buf.policies.%s to pin to skip the lookup", err, pn.key)
		}
		if to == "" || to == pn.version {
			continue
		}
		plan.Changes = append(plan.Changes, edit.Change{File: pn.file, Line: pn.line, Span: pn.span, From: pn.version, To: to})
		changed[pn.file] = true
	}
	plan.Templates = slices.Sorted(maps.Keys(changed))

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
