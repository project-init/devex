package skill

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const skillName = "run-discovery"

//go:embed run-discovery
var bundled embed.FS

type Harness string

const (
	HarnessCodex  Harness = "codex"
	HarnessClaude Harness = "claude"
	HarnessCursor Harness = "cursor"
)

var allHarnesses = []Harness{HarnessCodex, HarnessClaude, HarnessCursor}

var harnessDirectories = map[Harness]string{
	HarnessCodex:  filepath.Join(".agents", "skills"),
	HarnessClaude: filepath.Join(".claude", "skills"),
	HarnessCursor: filepath.Join(".cursor", "skills"),
}

func ParseHarnesses(values []string) ([]Harness, error) {
	selected := make(map[Harness]struct{})
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			switch Harness(strings.ToLower(strings.TrimSpace(candidate))) {
			case "all":
				for _, harness := range allHarnesses {
					selected[harness] = struct{}{}
				}
			case HarnessCodex, HarnessClaude, HarnessCursor:
				selected[Harness(strings.ToLower(strings.TrimSpace(candidate)))] = struct{}{}
			default:
				return nil, fmt.Errorf("unsupported AI harness %q; use codex, claude, cursor, or all", candidate)
			}
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("at least one AI harness is required")
	}
	result := make([]Harness, 0, len(selected))
	for _, harness := range allHarnesses {
		if _, exists := selected[harness]; exists {
			result = append(result, harness)
		}
	}
	return result, nil
}

func Install(projectDirectory string, harnesses []Harness, force bool) ([]string, error) {
	if len(harnesses) == 0 {
		return nil, fmt.Errorf("at least one AI harness is required")
	}
	projectRoot, err := filepath.Abs(projectDirectory)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory %s: %w", projectDirectory, err)
	}
	info, err := os.Stat(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect project directory %s: %w", projectRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project directory %s is not a directory", projectRoot)
	}
	files, err := skillFiles()
	if err != nil {
		return nil, err
	}
	targets := make([]installTarget, 0, len(harnesses)*len(files))
	installedRoots := make([]string, 0, len(harnesses))
	for _, harness := range harnesses {
		base, exists := harnessDirectories[harness]
		if !exists {
			return nil, fmt.Errorf("unsupported AI harness %q", harness)
		}
		root := filepath.Join(projectRoot, base, skillName)
		installedRoots = append(installedRoots, root)
		for relativePath, content := range files {
			targets = append(targets, installTarget{
				path:    filepath.Join(root, filepath.FromSlash(relativePath)),
				content: content,
			})
		}
	}
	if err := preflight(targets, force); err != nil {
		return nil, err
	}
	for _, target := range targets {
		if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
			return nil, fmt.Errorf("create skill directory for %s: %w", target.path, err)
		}
		if err := os.WriteFile(target.path, target.content, 0o644); err != nil {
			return nil, fmt.Errorf("write skill file %s: %w", target.path, err)
		}
	}
	return installedRoots, nil
}

type installTarget struct {
	path    string
	content []byte
}

func skillFiles() (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(bundled, skillName, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(skillName, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relativePath)] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read bundled discovery skill: %w", err)
	}
	return files, nil
}

func preflight(targets []installTarget, force bool) error {
	sort.Slice(targets, func(i, j int) bool { return targets[i].path < targets[j].path })
	for _, target := range targets {
		existing, err := os.ReadFile(target.path)
		if err == nil {
			if bytes.Equal(existing, target.content) || force {
				continue
			}
			return fmt.Errorf("skill file %s already exists with different contents; use --force to replace it", target.path)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("read skill file %s: %w", target.path, err)
		}
	}
	return nil
}
