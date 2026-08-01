package skill

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
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
	codexSkill := filepath.Join(project, ".agents", "skills", skillName, "SKILL.md")
	if _, err := os.Stat(codexSkill); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".cursor", "skills", skillName, "references", "work-breakdown.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexSkill, []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(project, []Harness{HarnessCodex}, false); err == nil {
		t.Fatal("Install() overwrote a modified skill")
	}
	if _, err := Install(project, []Harness{HarnessCodex}, true); err != nil {
		t.Fatal(err)
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
