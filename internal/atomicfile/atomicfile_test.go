package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// umasked is the mode os.Create gives a new file under this process's umask.
func umasked(t *testing.T) os.FileMode {
	t.Helper()
	reference, err := os.Create(filepath.Join(t.TempDir(), "reference"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := reference.Stat()
	_ = reference.Close()
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestWriteReplacesContentAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipt.yaml")
	for _, content := range []string{"first", "second"} {
		if err := Write(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Errorf("content = %q, %v; want %q", data, err, content)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only receipt.yaml", len(entries))
	}
}

func TestWriteKeepsAReplacedFilesModeAndHonorsTheUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes carry no group or other bits")
	}
	dir := t.TempDir()
	fresh, private := filepath.Join(dir, "new.csv"), filepath.Join(dir, "private.csv")
	if err := os.WriteFile(private, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{fresh: umasked(t), private: 0o600} {
		if err := Write(path, []byte("data")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v", filepath.Base(path), info.Mode().Perm(), want)
		}
	}
}

func TestWriteSkipsAKilledRunsLeftover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devex.csv")
	// The name the next write would take, left behind by a killed run with this process ID.
	if err := os.WriteFile(tempName(path, sequence.Load()+1), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("data")); err != nil {
		t.Errorf("Write() = %v, want the leftover skipped", err)
	}
}

func TestWriteLeavesOtherFilesAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devex.csv")
	other := filepath.Join(filepath.Dir(path), ".devex.csv.1-1"+tempSuffix) // another run's file
	if err := os.WriteFile(other, []byte("another run's csv"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "another run's csv" {
		t.Errorf("other run's file = %q, %v; want it untouched", data, err)
	}
}

func TestWriteReplacesASymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	outside, link := filepath.Join(dir, "outside"), filepath.Join(dir, "receipt.yaml")
	if err := os.WriteFile(outside, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A repository can check in receipt.yaml as a link to a file elsewhere.
	if err := os.Symlink("outside", link); err != nil {
		t.Skip(err)
	}
	if err := Write(link, []byte("receipt")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "precious" {
		t.Errorf("link target = %q, %v; want it untouched", data, err)
	}
	if data, err := os.ReadFile(link); err != nil || string(data) != "receipt" {
		t.Errorf("receipt.yaml = %q, %v; want the new content in a regular file", data, err)
	}
}

func TestWriteNeverOpensASymlinksReplacementWiderThanItsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes carry no group or other bits")
	}
	// The cases tell narrowing apart only when the umask default sits strictly between them.
	u := umasked(t)
	if u == 0o666 || u == 0o600 {
		t.Skipf("umask default %v matches a target mode", u)
	}
	for name, target := range map[string]os.FileMode{"world-writable target": 0o666, "private target": 0o600} {
		dir := t.TempDir()
		pointed, link := filepath.Join(dir, "pointed"), filepath.Join(dir, "receipt.yaml")
		if err := os.WriteFile(pointed, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(pointed, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("pointed", link); err != nil {
			t.Skip(err)
		}
		if err := Write(link, []byte("receipt")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(link)
		if err != nil {
			t.Fatal(err)
		}
		if want := u & target; info.Mode().Perm() != want {
			t.Errorf("%s: receipt.yaml mode = %v, want %v", name, info.Mode().Perm(), want)
		}
	}
}
