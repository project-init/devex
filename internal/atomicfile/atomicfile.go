// Package atomicfile replaces files so a reader, or the next run after a crash, sees either the
// old content or the new, never a mix.
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
)

// tempSuffix ends every temporary file Write creates beside its target.
const tempSuffix = ".tmp"

// sequence keeps concurrent writes in one process apart.
var sequence atomic.Int64

// Write replaces path with data through a temporary file in the same directory, synced before
// the rename. A replaced file keeps its permission bits; a new one gets the mode os.Create gives
// it, under the umask. A symlink at path itself is replaced, never written through, and its
// replacement is no more open than the file it pointed at, when that file exists. Symlinks
// among path's parent directories are followed as usual. The directory must exist. The temporary
// file is hidden and ends in .tmp, so a reader that lists the directory by the target's
// extension skips one a killed run left behind.
func Write(path string, data []byte) error {
	temp, err := create(path)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	// Set the mode before the data arrives, so a private file is never readable more widely.
	if mode, set := replacementMode(path, temp); set {
		err = temp.Chmod(mode)
	}
	if err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// replacementMode is the mode the file replacing path needs, if not temp's own. A regular file
// passes its permissions on. A symlink's replacement keeps temp's umask default, narrowed to the
// permissions of the file the link pointed at, so the link can neither widen nor expose access.
// Any failure to read path leaves temp's mode, and surfaces at the rename if it matters.
func replacementMode(path string, temp *os.File) (os.FileMode, bool) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return 0, false
	case info.Mode().IsRegular():
		return info.Mode().Perm(), true
	case info.Mode()&os.ModeSymlink == 0:
		return 0, false
	}
	target, err := os.Stat(path)
	own, ownErr := temp.Stat()
	if err != nil || ownErr != nil {
		return 0, false
	}
	return own.Mode().Perm() & target.Mode().Perm(), true
}

// syncDir flushes the rename to disk where the platform allows it. The rename has already
// happened, so a failure costs only durability across a power loss.
func syncDir(dir string) {
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
}

// create opens a new temporary file beside path. Mode 0666 lets the umask apply, as os.Create
// does; os.CreateTemp would make it owner-only. It skips a name a killed run left behind.
func create(path string) (*os.File, error) {
	for range 10 {
		temp, err := os.OpenFile(tempName(path, sequence.Add(1)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if !errors.Is(err, fs.ErrExist) {
			return temp, err
		}
	}
	return nil, fmt.Errorf("create a temporary file beside %s: too many leftovers", path)
}

// tempName is a hidden name beside path that names this process.
func tempName(path string, n int64) string {
	return filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.%d-%d%s", filepath.Base(path), os.Getpid(), n, tempSuffix))
}
