package pins

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DefaultExclude skips fixtures and vendored code, which carry versions the project does
// not own.
var DefaultExclude = []string{"testdata/", "vendor/"}

// DefaultImages matches the official golang image under any registry prefix.
var DefaultImages = []string{"golang"}

// maxScanBytes bounds the files read for the ARG-shadowing search.
const maxScanBytes = 1 << 20

// Options configures pin discovery.
type Options struct {
	// Root is the repository directory.
	Root string
	// Images lists the container images that carry the Go toolchain.
	Images []string
	// Exclude lists globs to skip, applied after DefaultExclude.
	Exclude []string
	// Declared lists version locations the built-in finders cannot recognize.
	Declared []Declared
	// ListFiles returns the candidate files as slash-separated paths relative to Root.
	// Nil uses GitFiles.
	ListFiles func(root string) ([]string, error)
}

// Result is every pin discovery found.
type Result struct {
	// Pins lists each pin, ordered by file and line.
	Pins []Pin
	// Modules lists the directories holding a go.mod file, relative to Root.
	Modules []string
	// Warnings lists versions discovery saw but cannot manage.
	Warnings []string
	// Files lists every candidate file discovery considered, after exclusions.
	Files []string
	// Workspaces maps each go.work to the module directories its use directives name, all
	// relative to Root.
	Workspaces map[string][]string
}

// Discover finds every Go version pin under opts.Root.
func Discover(opts Options) (Result, error) {
	images := opts.Images
	if len(images) == 0 {
		images = DefaultImages
	}
	for _, pattern := range images {
		if _, err := path.Match(pattern, ""); err != nil {
			return Result{}, fmt.Errorf("image pattern %q: %w", pattern, err)
		}
	}
	exclude, err := compileGlobs(append(slices.Clone(DefaultExclude), opts.Exclude...))
	if err != nil {
		return Result{}, err
	}
	list := opts.ListFiles
	if list == nil {
		list = GitFiles
	}
	files, err := list(opts.Root)
	if err != nil {
		return Result{}, err
	}

	type spanKey struct {
		file  string
		start int
	}
	res := Result{Workspaces: map[string][]string{}}
	seen := map[spanKey]bool{}
	for _, file := range files {
		if matchAny(exclude, file) {
			continue
		}
		res.Files = append(res.Files, file)

		finders := findersFor(file, images, opts.Declared)
		if len(finders) == 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(opts.Root, filepath.FromSlash(file)))
		if errors.Is(err, fs.ErrNotExist) {
			// git ls-files still lists tracked files deleted from the working tree.
			continue
		}
		if err != nil {
			return Result{}, err
		}
		if path.Base(file) == "go.mod" {
			res.Modules = append(res.Modules, path.Dir(file))
		}
		for _, find := range finders {
			f, err := find(file, data)
			if err != nil {
				return Result{}, err
			}
			if f.uses != nil {
				res.Workspaces[file] = f.uses
			}
			res.Warnings = append(res.Warnings, f.warnings...)
			for _, p := range f.pins {
				if key := (spanKey{p.File, p.Span.Start}); !seen[key] {
					seen[key] = true
					res.Pins = append(res.Pins, p)
				}
			}
		}
	}

	slices.SortFunc(res.Pins, func(a, b Pin) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Span.Start, b.Span.Start))
	})

	return res, nil
}

// finding is what a finder reads from one file.
type finding struct {
	pins     []Pin
	warnings []string
	// uses lists a go.work's module directories, relative to the repository root.
	uses []string
}

type finder func(file string, data []byte) (finding, error)

// lenient adapts a finder that reports every problem as a warning.
func lenient(find func(file string, data []byte) ([]Pin, []string)) finder {
	return func(file string, data []byte) (finding, error) {
		pins, warnings := find(file, data)

		return finding{pins: pins, warnings: warnings}, nil
	}
}

// findersFor returns the finders that apply to file. Built-in finders come first, so they
// win when a declared pin overlaps one.
func findersFor(file string, images []string, declared []Declared) []finder {
	var finders []finder
	switch base := path.Base(file); {
	case base == "go.mod":
		finders = append(finders, findGoMod)
	case base == "go.work":
		finders = append(finders, findGoWork)
	case base == ".go-version":
		finders = append(finders, lenient(findGoVersionFile))
	case base == ".tool-versions":
		finders = append(finders, lenient(findToolVersions))
	case isMiseConfig(file):
		finders = append(finders, lenient(findMise))
	case isDockerfile(file):
		finders = append(finders, lenient(func(f string, d []byte) ([]Pin, []string) { return findDockerfile(f, d, images) }))
	case isWorkflow(file):
		finders = append(finders, lenient(findSetupGo))
	}
	for _, d := range declared {
		if matchAny(d.files, file) {
			finders = append(finders, lenient(func(f string, data []byte) ([]Pin, []string) { return findDeclared(f, data, d) }))
		}
	}

	return finders
}

// ShadowWarnings flags other files that mention an ARG supplying a Dockerfile's version. A
// --build-arg set there overrides the Dockerfile default, and neither upgrade nor check can
// see it unless the project declares it as a pin.
func ShadowWarnings(root string, res Result) []string {
	pinLines := map[string]bool{}
	args := map[string]Pin{}
	for _, p := range res.Pins {
		pinLines[p.Location()] = true
		if _, ok := args[p.Arg]; p.Arg != "" && !ok {
			args[p.Arg] = p
		}
	}
	if len(args) == 0 {
		return nil
	}

	var names []string
	for _, name := range slices.Sorted(maps.Keys(args)) {
		names = append(names, regexp.QuoteMeta(name))
	}
	mention := regexp.MustCompile(`\b(` + strings.Join(names, "|") + `)\b`)

	var warnings []string
	for _, file := range res.Files {
		// A Dockerfile cannot pass build args to another, and prose cannot pass them at all.
		if isDockerfile(file) || isProse(file) {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(file))
		if info, err := os.Stat(full); err != nil || info.Size() > maxScanBytes {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil || bytes.IndexByte(data, 0) >= 0 || !mention.Match(data) {
			continue
		}
		for l := range numberedLines(data) {
			m := mention.FindStringSubmatch(l.text)
			if m == nil {
				continue
			}
			trimmed := strings.TrimSpace(l.text)
			loc := fmt.Sprintf("%s:%d", file, l.no)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") || pinLines[loc] {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("%s: %s also appears in %s; if that passes a --build-arg, declare it as a pin", args[m[1]].Location(), m[1], loc))
		}
	}

	return warnings
}

func isProse(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".mdx", ".rst", ".txt":
		return true
	}

	return false
}

// GitFiles lists tracked and untracked files that .gitignore does not exclude. Outside a git
// repository it walks the directory tree instead.
func GitFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z").Output()
	if err != nil {
		return WalkFiles(root)
	}

	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}

	return files, nil
}

// WalkFiles lists every file under root except the .git directory.
func WalkFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}

			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))

		return nil
	})

	return files, err
}
