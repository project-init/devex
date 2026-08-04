package skill

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseHarnesses(t *testing.T) {
	harnesses, err := ParseHarnesses([]string{"cursor,codex", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Harness{HarnessCodex, HarnessClaude, HarnessCursor}
	if len(harnesses) != len(want) {
		t.Fatalf("harnesses = %#v", harnesses)
	}
	for index := range want {
		if harnesses[index] != want[index] {
			t.Fatalf("harnesses = %#v", harnesses)
		}
	}
	if _, err := ParseHarnesses([]string{"unknown"}); err == nil {
		t.Fatal("ParseHarnesses() accepted an unsupported harness")
	}
}

func TestInstallCopiesSkillAndProtectsChanges(t *testing.T) {
	project := t.TempDir()
	installed, err := Install(project, []Harness{HarnessCodex, HarnessCursor}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed = %#v", installed)
	}
	for _, result := range installed {
		if result.State != InstallStateInstalled {
			t.Fatalf("result = %#v", result)
		}
	}
	codexSkill := filepath.Join(project, ".agents", "skills", skillName, "SKILL.md")
	if _, err := os.Stat(codexSkill); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "skills", skillName, "references", "work-breakdown.md")); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Unix(100, 0)
	if err := os.Chtimes(codexSkill, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	unchanged, err := Install(project, []Harness{HarnessCodex}, false)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged[0].State != InstallStateUnchanged {
		t.Fatalf("unchanged result = %#v", unchanged[0])
	}
	info, err := os.Stat(codexSkill)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Fatalf("unchanged install rewrote %s", codexSkill)
	}
	if err := os.WriteFile(codexSkill, []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(project, []Harness{HarnessCodex}, false); err == nil {
		t.Fatal("Install() overwrote a modified skill")
	}
	updated, err := Install(project, []Harness{HarnessCodex}, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated[0].State != InstallStateUpdated {
		t.Fatalf("updated result = %#v", updated[0])
	}
}

func TestInspectAndDetectHarnesses(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	detected, err := DetectHarnesses(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(detected) != 1 || detected[0] != HarnessClaude {
		t.Fatalf("detected = %#v", detected)
	}
	statuses, err := Inspect(project, []Harness{HarnessClaude})
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != StateMissing {
		t.Fatalf("status = %#v", statuses[0])
	}
	if _, err := Install(project, []Harness{HarnessClaude}, false); err != nil {
		t.Fatal(err)
	}
	statuses, err = Inspect(project, []Harness{HarnessClaude})
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != StateCurrent {
		t.Fatalf("status = %#v", statuses[0])
	}
	if err := os.WriteFile(filepath.Join(statuses[0].Path, "SKILL.md"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statuses, err = Inspect(project, []Harness{HarnessClaude})
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != StateModified {
		t.Fatalf("status = %#v", statuses[0])
	}
}

func TestBundledSkillMatchesRepositorySkill(t *testing.T) {
	err := fs.WalkDir(bundled, skillName, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		bundledContent, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(skillName, path)
		if err != nil {
			return err
		}
		repositoryPath := filepath.Join("..", "..", "..", ".agents", "skills", skillName, relativePath)
		repositoryContent, err := os.ReadFile(repositoryPath)
		if err != nil {
			return err
		}
		if !bytes.Equal(bundledContent, repositoryContent) {
			t.Fatalf("bundled %s differs from %s", path, repositoryPath)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
