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

type State string

const (
	StateMissing  State = "missing"
	StateCurrent  State = "current"
	StateModified State = "modified"
)

type Status struct {
	Harness Harness
	Path    string
	State   State
}

type InstallState string

const (
	InstallStateInstalled InstallState = "installed"
	InstallStateUnchanged InstallState = "unchanged"
	InstallStateUpdated   InstallState = "updated"
)

type InstallResult struct {
	Harness Harness
	Path    string
	State   InstallState
}

var allHarnesses = []Harness{HarnessCodex, HarnessClaude, HarnessCursor}

var harnessDirectories = map[Harness]string{
	HarnessCodex:  filepath.Join(".agents", "skills"),
	HarnessClaude: filepath.Join(".claude", "skills"),
	HarnessCursor: filepath.Join(".cursor", "skills"),
}

func AllHarnesses() []Harness {
	return append([]Harness(nil), allHarnesses...)
}

func ParseHarnesses(values []string) ([]Harness, error) {
	selected := make(map[Harness]struct{})
	for _, value := range values {
		for _, candidate := range strings.Split(value, ",") {
			normalized := Harness(strings.ToLower(strings.TrimSpace(candidate)))
			switch normalized {
			case "all":
				for _, harness := range allHarnesses {
					selected[harness] = struct{}{}
				}
			case HarnessCodex, HarnessClaude, HarnessCursor:
				selected[normalized] = struct{}{}
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

func DetectHarnesses(projectDirectory string) ([]Harness, error) {
	projectRoot, err := resolveProjectRoot(projectDirectory)
	if err != nil {
		return nil, err
	}
	detected := make([]Harness, 0, len(allHarnesses))
	for _, harness := range allHarnesses {
		path := filepath.Join(projectRoot, filepath.Dir(harnessDirectories[harness]))
		if _, err := os.Stat(path); err == nil {
			detected = append(detected, harness)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect AI harness directory %s: %w", path, err)
		}
	}
	return detected, nil
}

func Inspect(projectDirectory string, harnesses []Harness) ([]Status, error) {
	if len(harnesses) == 0 {
		return nil, fmt.Errorf("at least one AI harness is required")
	}
	projectRoot, err := resolveProjectRoot(projectDirectory)
	if err != nil {
		return nil, err
	}
	files, err := skillFiles()
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(harnesses))
	for _, harness := range harnesses {
		root, err := harnessSkillRoot(projectRoot, harness)
		if err != nil {
			return nil, err
		}
		state, err := inspectRoot(root, files)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, Status{Harness: harness, Path: root, State: state})
	}
	return statuses, nil
}

func Install(projectDirectory string, harnesses []Harness, force bool) ([]InstallResult, error) {
	if len(harnesses) == 0 {
		return nil, fmt.Errorf("at least one AI harness is required")
	}
	projectRoot, err := resolveProjectRoot(projectDirectory)
	if err != nil {
		return nil, err
	}
	files, err := skillFiles()
	if err != nil {
		return nil, err
	}
	targets := make([]installTarget, 0, len(harnesses)*len(files))
	results := make([]InstallResult, 0, len(harnesses))
	resultIndexes := make(map[Harness]int, len(harnesses))
	for _, harness := range harnesses {
		root, err := harnessSkillRoot(projectRoot, harness)
		if err != nil {
			return nil, err
		}
		resultIndexes[harness] = len(results)
		results = append(results, InstallResult{Harness: harness, Path: root, State: InstallStateUnchanged})
		for relativePath, content := range files {
			targets = append(targets, installTarget{
				harness: harness,
				path:    filepath.Join(root, filepath.FromSlash(relativePath)),
				content: content,
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].path < targets[j].path })
	for index := range targets {
		target := &targets[index]
		existing, readErr := os.ReadFile(target.path)
		switch {
		case readErr == nil && bytes.Equal(existing, target.content):
			continue
		case readErr == nil && !force:
			return nil, fmt.Errorf("skill file %s already exists with different contents; use --force to replace it", target.path)
		case readErr == nil:
			target.write = true
			results[resultIndexes[target.harness]].State = InstallStateUpdated
		case os.IsNotExist(readErr):
			target.write = true
			result := &results[resultIndexes[target.harness]]
			if result.State == InstallStateUnchanged {
				result.State = InstallStateInstalled
			}
		default:
			return nil, fmt.Errorf("read skill file %s: %w", target.path, readErr)
		}
	}
	for _, target := range targets {
		if !target.write {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
			return nil, fmt.Errorf("create skill directory for %s: %w", target.path, err)
		}
		if err := os.WriteFile(target.path, target.content, 0o644); err != nil {
			return nil, fmt.Errorf("write skill file %s: %w", target.path, err)
		}
	}
	return results, nil
}

type installTarget struct {
	harness Harness
	path    string
	content []byte
	write   bool
}

func resolveProjectRoot(projectDirectory string) (string, error) {
	projectRoot, err := filepath.Abs(projectDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve project directory %s: %w", projectDirectory, err)
	}
	info, err := os.Stat(projectRoot)
	if err != nil {
		return "", fmt.Errorf("inspect project directory %s: %w", projectRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project directory %s is not a directory", projectRoot)
	}
	return projectRoot, nil
}

func harnessSkillRoot(projectRoot string, harness Harness) (string, error) {
	base, exists := harnessDirectories[harness]
	if !exists {
		return "", fmt.Errorf("unsupported AI harness %q", harness)
	}
	return filepath.Join(projectRoot, base, skillName), nil
}

func inspectRoot(root string, files map[string][]byte) (State, error) {
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return StateMissing, nil
	} else if err != nil {
		return "", fmt.Errorf("inspect skill directory %s: %w", root, err)
	}
	if !info.IsDir() {
		return StateModified, nil
	}
	for relativePath, expected := range files {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		actual, err := os.ReadFile(path)
		if os.IsNotExist(err) || (err == nil && !bytes.Equal(actual, expected)) {
			return StateModified, nil
		}
		if err != nil {
			return "", fmt.Errorf("read skill file %s: %w", path, err)
		}
	}
	return StateCurrent, nil
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
