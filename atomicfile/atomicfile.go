// Package atomicfile writes small state files so that a crash or power loss
// at any instant leaves either the previous or the new content on disk, never
// a torn file. It is tuned for flash storage: unchanged content is not
// rewritten, and the last successful save is kept as <path>.bak (with the
// one before it as <path>.bak.1) so a file corrupted after the fact can be
// recovered on the next start without losing an acknowledged save.
//
// Write sequence: write the data to two temp files in <dir> (fsync both),
// rename the first over <path>, then refresh the backups (rename <path>.bak
// to <path>.bak.1, rename the second temp file to <path>.bak), and fsync the
// directory. Nothing is acknowledged before that fsync, so after Write
// returns <path> and <path>.bak hold the same content and <path>.bak.1 holds
// the previous generation. Before Write returns, every on-disk state is
// either the previous generation (complete) or the new one. The backup is a
// separate inode, never a hard link: an in-place overwrite or a media error
// on the primary must not reach it.
package atomicfile

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BackupSuffix is appended to the path of the copy of the last successful
// save. It always equals the primary after a completed Write.
const BackupSuffix = ".bak"

// PreviousSuffix is appended to the path of the generation before the last
// successful save (the save before the one in <path>.bak).
const PreviousSuffix = ".bak.1"

// ErrUnchanged is returned by Write when the content already on disk is
// byte-identical and nothing was written. Callers may treat it as success.
var ErrUnchanged = errors.New("atomicfile: content unchanged")

// Options tune Write.
type Options struct {
	// Perm is the mode of the new file (default 0600).
	Perm fs.FileMode
	// NoBackup skips keeping <path>.bak and <path>.bak.1.
	NoBackup bool
	// Force writes even when the content is identical.
	Force bool
}

// Write steps, in order, as reported to the test hook.
const (
	stepTempWritten = "temp-written" // temp files hold the data, fsynced
	stepRenamed     = "renamed"      // the primary holds the new data
	stepRotated     = "rotated"      // .bak moved to .bak.1
	stepBackup      = "backup"       // .bak holds the new data
)

// stepHook, when set by a test, runs after each step of Write. A non-nil
// error aborts Write there, simulating a crash at that instant (nothing after
// it runs, including the directory fsync).
var stepHook func(step string) error

func afterStep(step string) error {
	if stepHook == nil {
		return nil
	}
	return stepHook(step)
}

// Write atomically replaces path with data. It returns ErrUnchanged (and
// touches nothing) when the file already holds exactly data, unless
// opts.Force is set. The parent directory is created with mode 0700.
func Write(path string, data []byte, opts Options) error {
	perm := opts.Perm
	if perm == 0 {
		perm = 0o600
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	current, readErr := os.ReadFile(path)
	exists := readErr == nil
	if exists && !opts.Force && sameContent(current, data) {
		return ErrUnchanged
	}

	base := filepath.Base(path)
	tmpName, err := writeTemp(dir, "."+base+".tmp-*", data, perm)
	if err != nil {
		return err
	}
	var tmpBackup string
	if !opts.NoBackup {
		if tmpBackup, err = writeTemp(dir, "."+base+BackupSuffix+".tmp-*", data, perm); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
	}
	cleanup := func() {
		_ = os.Remove(tmpName)
		if tmpBackup != "" {
			_ = os.Remove(tmpBackup)
		}
	}
	if err = afterStep(stepTempWritten); err != nil {
		cleanup()
		return err
	}

	if err = os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	if err = afterStep(stepRenamed); err != nil {
		return err
	}

	if tmpBackup != "" {
		if err = refreshBackup(path, tmpBackup); err != nil {
			_ = os.Remove(tmpBackup)
			return err
		}
	}
	return SyncDir(dir)
}

// writeTemp writes data to a new temp file in dir, fsyncs and closes it, and
// returns its name.
func writeTemp(dir, pattern string, data []byte, perm fs.FileMode) (string, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if err = tmp.Chmod(perm); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// refreshBackup makes <path>.bak hold what path holds now (tmpBackup is a
// fsynced copy of it) and keeps the previous .bak as <path>.bak.1. It runs
// after the primary rename, so the backup is never newer than the primary
// and, once Write returns, never older either.
func refreshBackup(path, tmpBackup string) error {
	bak, previous := path+BackupSuffix, path+PreviousSuffix
	if err := os.Rename(bak, previous); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("atomicfile: rotate backup: %w", err)
	}
	if err := afterStep(stepRotated); err != nil {
		return err
	}
	if err := os.Rename(tmpBackup, bak); err != nil {
		return fmt.Errorf("atomicfile: backup: %w", err)
	}
	return afterStep(stepBackup)
}

// WriteFile is Write with default options, treating ErrUnchanged as success.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	err := Write(path, data, Options{Perm: perm})
	if errors.Is(err, ErrUnchanged) {
		return nil
	}
	return err
}

// Read returns the content of path, or of the newest backup (path.bak, then
// path.bak.1) when path is missing or fails validate. recovered reports that
// a backup was used; the caller should log it and may re-Write to heal the
// primary. validate may be nil.
//
// When no copy exists, the error satisfies errors.Is(err, fs.ErrNotExist).
func Read(path string, validate func([]byte) error) (data []byte, recovered bool, err error) {
	return Decode(path, func(b []byte) ([]byte, error) {
		if validate != nil {
			if err := validate(b); err != nil {
				return nil, err
			}
		}
		return b, nil
	})
}

// Decode is Read for callers that parse the file: decode runs on the primary
// and, when that is missing or decode rejects it, on each backup in turn, so
// the file is parsed once rather than validated and then parsed again.
func Decode[T any](path string, decode func([]byte) (T, error)) (value T, recovered bool, err error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if value, err = decode(data); err == nil {
			return value, false, nil
		}
		err = fmt.Errorf("%s: %w", path, err)
	}
	primaryErr := err

	var backupErrs []error
	for _, suffix := range []string{BackupSuffix, PreviousSuffix} {
		bak, berr := os.ReadFile(path + suffix)
		if berr != nil {
			if !errors.Is(berr, fs.ErrNotExist) {
				backupErrs = append(backupErrs, fmt.Errorf("%s: %w", path+suffix, berr))
			}
			continue
		}
		if value, err = decode(bak); err == nil {
			return value, true, nil
		}
		backupErrs = append(backupErrs, fmt.Errorf("%s: %w", path+suffix, err))
	}
	var zero T
	if len(backupErrs) == 0 {
		return zero, false, primaryErr
	}
	return zero, false, fmt.Errorf("%w (backup also invalid: %v)", primaryErr, errors.Join(backupErrs...))
}

// SyncDir fsyncs a directory so a completed rename is durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// Some filesystems (e.g. certain FUSE/9p/vfat mounts) reject
		// directory fsync; the rename itself is still atomic there.
		if errors.Is(err, errors.ErrUnsupported) || isEINVAL(err) {
			return nil
		}
		return err
	}
	return nil
}

func sameContent(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) <= 64<<10 {
		return bytes.Equal(a, b)
	}
	ha, hb := sha256.Sum256(a), sha256.Sum256(b)
	return ha == hb
}
