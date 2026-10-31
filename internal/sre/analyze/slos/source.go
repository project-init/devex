package slos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/project-init/devex/internal/sre/config"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"gopkg.in/yaml.v3"
)

// fileVersion is the one SLO file shape this devex reads. The file changes its version only on a change that
// breaks a reader, so a new field keeps it at 1.
const fileVersion = 1

// endpoint is one RPC's SLO from the SLO file.
type endpoint struct {
	Method string // full method, e.g. /acme.orders.v1.OrdersService/GetOrder
	// SLO is the response time SLO in milliseconds. It is 0 when the RPC has none.
	SLO float64
	// Metric names the RPC's metrics without the prefix, e.g. acmeOrdersV1OrdersServiceGetOrder.
	Metric string
	// Prefix is the metric namespace of the app that serves the RPC, e.g. acme. It comes from Datadog, because the
	// app that serves an RPC is not something the protos know. It is empty when no app reports the metric.
	Prefix string
	Scope  string   // tag scope of every query, e.g. env:production
	Tags   []string // x-slo-tags, e.g. p0
	// Rollup is the bucket size of the RPC's queries: its x-slo-evaluation-window, or --rollup.
	Rollup time.Duration
}

// MetricName is the metric namespace the queries read, e.g. acme.acmeOrdersV1OrdersServiceGetOrder.
func (e endpoint) MetricName() string {
	return e.Prefix + "." + e.Metric
}

// Name is the method as the report shows it: /acme.orders.v1.OrdersService/GetOrder -> OrdersService.GetOrder.
func (e endpoint) Name() string {
	svc, method, ok := strings.Cut(strings.TrimPrefix(e.Method, "/"), "/")
	if !ok {
		return e.Method
	}
	return svc[strings.LastIndex(svc, ".")+1:] + "." + method
}

type sloFile struct {
	Version  int                   `yaml:"version"`
	Services map[string][]sloEntry `yaml:"services"`
}

type sloEntry struct {
	Method string   `yaml:"method"`
	Metric string   `yaml:"metric"`
	SLO    *float64 `yaml:"slo"`
	Tags   []string `yaml:"tags"`
	// EvaluationWindow is the x-slo-evaluation-window, e.g. 1h. It is empty on almost every RPC.
	EvaluationWindow string `yaml:"evaluationWindow"`
}

// parseSLOs reads the RPCs under services.<group>, in file order. It checks version before anything else, so a
// file in a shape this devex does not know fails with that, not with a decode error. rollup is the bucket size for
// every RPC that does not set its own evaluation window.
func parseSLOs(data []byte, group, scope string, rollup time.Duration) ([]endpoint, error) {
	var head struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("read the SLO file: %w", err)
	}
	if head.Version != fileVersion {
		return nil, fmt.Errorf("the SLO file is version %d, and devex %s reads version %d only: upgrade devex to a release that reads version %d",
			head.Version, devexVersion(), fileVersion, head.Version)
	}

	var f sloFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("read the SLO file: %w", err)
	}
	groups := strings.Join(slices.Sorted(maps.Keys(f.Services)), ", ")
	if group == "" {
		return nil, fmt.Errorf("pass --group: the SLO file holds %s", groups)
	}
	entries, ok := f.Services[group]
	if !ok {
		return nil, fmt.Errorf("the SLO file has no group %q: pass --group with one of %s", group, groups)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("group %q has no RPCs in the SLO file: it owns no service yet", group)
	}
	eps := make([]endpoint, 0, len(entries))
	for _, e := range entries {
		ep := endpoint{Method: e.Method, Metric: e.Metric, Scope: scope, Tags: e.Tags, Rollup: rollup}
		if e.SLO != nil {
			ep.SLO = *e.SLO
		}
		if e.EvaluationWindow != "" {
			window, err := time.ParseDuration(e.EvaluationWindow)
			if err != nil {
				return nil, fmt.Errorf("%s has evaluationWindow %q, which is not a duration", e.Method, e.EvaluationWindow)
			}
			ep.Rollup = window
		}
		eps = append(eps, ep)
	}
	return eps, nil
}

// filterByTags keeps the RPCs that carry any of tags. No tags keeps every RPC.
func filterByTags(eps []endpoint, tags []string) []endpoint {
	if len(tags) == 0 {
		return eps
	}
	out := make([]endpoint, 0, len(eps))
	for _, ep := range eps {
		if slices.ContainsFunc(ep.Tags, func(t string) bool { return slices.Contains(tags, t) }) {
			out = append(out, ep)
		}
	}
	return out
}

func devexVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// moduleRef is where the SLO file comes from: a module version to download, or a local directory that a replace
// points to.
type moduleRef struct {
	Path, Version string
	Dir           string // set for a replace to a local path; Path and Version are then empty
}

func (m moduleRef) String() string {
	if m.Dir != "" {
		return m.Dir
	}
	return m.Path + "@" + m.Version
}

// resolveModule finds the version of module that the go.mod at goModPath requires, and follows a replace of it.
// fallback is used when go.mod does not require module or does not exist.
func resolveModule(goModPath, module, fallback string) (moduleRef, error) {
	f, err := parseGoMod(goModPath)
	if errors.Is(err, fs.ErrNotExist) {
		if fallback == "" {
			return moduleRef{}, fmt.Errorf("no %s to read the %s version from: set slos.version", goModPath, module)
		}
		return moduleRef{Path: module, Version: fallback}, nil
	}
	if err != nil {
		return moduleRef{}, err
	}
	return requiredModule(f, goModPath, module, fallback)
}

func parseGoMod(goModPath string) (*modfile.File, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(goModPath, data, nil)
}

// requiredModule is resolveModule over a parsed go.mod.
func requiredModule(f *modfile.File, goModPath, module, fallback string) (moduleRef, error) {
	ref := moduleRef{Path: module, Version: fallback}
	for _, r := range f.Require {
		if r.Mod.Path == module {
			ref.Version = r.Mod.Version
		}
	}
	if ref.Version == "" {
		return moduleRef{}, fmt.Errorf("%s does not require %s: add it, or set slos.version", goModPath, module)
	}
	// A replace that names a version applies to that version only; one without applies to every version.
	for _, r := range f.Replace {
		if r.Old.Path != module || (r.Old.Version != "" && r.Old.Version != ref.Version) {
			continue
		}
		if r.New.Version == "" {
			dir := r.New.Path
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(filepath.Dir(goModPath), dir)
			}
			return moduleRef{Dir: dir}, nil
		}
		ref.Path, ref.Version = r.New.Path, r.New.Version
	}
	return ref, nil
}

// findSLOModule names the module that the go.mod at goModPath requires and that ships file, so a repository needs no
// setting to say where its SLOs come from. It looks only in local replaces and the module cache, so it asks the
// network nothing, and a module that is not downloaded yet is not found.
func findSLOModule(ctx context.Context, goModPath, file string, modCache func(context.Context) (string, error)) (string, error) {
	f, err := parseGoMod(goModPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no %s to find the module that ships %s in: pass --module", goModPath, file)
	}
	if err != nil {
		return "", err
	}
	cache, err := modCache(ctx)
	if err != nil {
		return "", err
	}

	var found []string
	for _, r := range f.Require {
		ref, err := requiredModule(f, goModPath, r.Mod.Path, "")
		if err != nil {
			return "", err
		}
		dir := ref.Dir
		if dir == "" {
			path, pathErr := module.EscapePath(ref.Path)
			version, versionErr := module.EscapeVersion(ref.Version)
			if pathErr != nil || versionErr != nil {
				continue
			}
			dir = filepath.Join(cache, path+"@"+version)
		}
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			found = append(found, r.Mod.Path)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("no module %s requires ships %s at the version it requires: run go mod download, bump the module to a release that ships it, or pass --module", goModPath, file)
	}
	return "", fmt.Errorf("%s each ship %s: pass --module to pick one", strings.Join(found, ", "), file)
}

// goModCache is where go mod download puts modules, from the user's GOMODCACHE or GOPATH.
func goModCache(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "env", "GOMODCACHE").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMODCACHE: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// downloader puts a module version in the module cache and returns its directory and the version it resolved to,
// so "latest" comes back as a real version.
type downloader func(ctx context.Context, path, version string) (dir, resolved string, err error)

// goModDownload runs go mod download, so the module comes through the user's GOPROXY, GOPRIVATE, and credentials.
func goModDownload(ctx context.Context, path, version string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "-json", path+"@"+version)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var out struct{ Dir, Version, Error string }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		if runErr != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return "", "", fmt.Errorf("go mod download %s@%s: %w: %s", path, version, runErr, msg)
			}
			return "", "", fmt.Errorf("go mod download %s@%s: %w", path, version, runErr)
		}
		return "", "", fmt.Errorf("go mod download %s@%s: %w", path, version, err)
	}
	if out.Error != "" {
		return "", "", fmt.Errorf("go mod download %s@%s: %s", path, version, out.Error)
	}
	if runErr != nil {
		return "", "", fmt.Errorf("go mod download %s@%s: %w", path, version, runErr)
	}
	return out.Dir, out.Version, nil
}

// readSLOFile reads cfg.File from the module the repository's go.mod pins. pin overrides that, and any replace of
// it, so --protos-version reads one release whatever the repository is built against. When the release has no SLO
// file, the error names the latest release if it has one.
func readSLOFile(ctx context.Context, cfg config.SLOsConfiguration, goModPath, pin string, download downloader) ([]byte, moduleRef, error) {
	ref := moduleRef{Path: cfg.Module, Version: pin}
	if pin == "" {
		var err error
		if ref, err = resolveModule(goModPath, cfg.Module, cfg.Version); err != nil {
			return nil, moduleRef{}, err
		}
	}
	var err error
	dir := ref.Dir
	if dir == "" {
		var resolved string
		if dir, resolved, err = download(ctx, ref.Path, ref.Version); err != nil {
			return nil, ref, fmt.Errorf("%w (if %s is private, check GOPRIVATE and your ~/.netrc)", err, ref.Path)
		}
		// So every message below names the release, not "latest".
		ref.Version = resolved
	}
	data, err := os.ReadFile(filepath.Join(dir, cfg.File))
	if !errors.Is(err, fs.ErrNotExist) {
		return data, ref, err
	}
	if ref.Dir != "" {
		return nil, ref, fmt.Errorf("%s has no %s: regenerate it there", ref.Dir, cfg.File)
	}

	latestDir, latest, err := download(ctx, ref.Path, "latest")
	if err != nil {
		return nil, ref, fmt.Errorf("%s has no %s, and the latest release did not download: %w", ref, cfg.File, err)
	}
	if _, err := os.Stat(filepath.Join(latestDir, cfg.File)); err == nil && latest != ref.Version {
		return nil, ref, fmt.Errorf("%s has no %s: bump it to %s or later (go get %s@%s)", ref, cfg.File, latest, ref.Path, latest)
	}
	return nil, ref, fmt.Errorf("%s has no %s, and no release of %s has it yet (latest %s)", ref, cfg.File, ref.Path, latest)
}
