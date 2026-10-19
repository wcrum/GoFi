package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.yaml")
	if err := Write(path, []byte("v1"), Options{}); err != nil {
		t.Fatal(err)
	}
	got, recovered, err := Read(path, nil)
	if err != nil || recovered || string(got) != "v1" {
		t.Fatalf("Read = %q %v %v", got, recovered, err)
	}
	// The backup always equals the last successful save, from the first one.
	if bak, err := os.ReadFile(path + BackupSuffix); err != nil || string(bak) != "v1" {
		t.Fatalf("backup after first write = %q %v", bak, err)
	}
	if _, err := os.Stat(path + PreviousSuffix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("first write must not create a previous generation: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}
}

func TestUnchangedIsNotRewritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := Write(path, []byte("same"), Options{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	err := Write(path, []byte("same"), Options{})
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("want ErrUnchanged, got %v", err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		t.Fatal("unchanged content must not touch the file")
	}
	if err := WriteFile(path, []byte("same"), 0o600); err != nil {
		t.Fatalf("WriteFile should swallow ErrUnchanged: %v", err)
	}
	if err := Write(path, []byte("same"), Options{Force: true}); err != nil {
		t.Fatalf("Force should write: %v", err)
	}
	// Large content takes the hash path.
	big := strings.Repeat("x", 200<<10)
	if err := Write(path, []byte(big), Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte(big), Options{}); !errors.Is(err, ErrUnchanged) {
		t.Fatalf("large unchanged: %v", err)
	}
}

// TestBackupIsNeverBehindAnAcknowledgedSave is the torn-write-after-rename
// case: the primary is corrupted after a save was acknowledged (flash error,
// stray write). Recovery must yield that save, not the one before it.
func TestBackupIsNeverBehindAnAcknowledgedSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	must := func(err error) {
		if err != nil {
			t.Helper()
			t.Fatal(err)
		}
	}
	must(Write(path, []byte("good-1"), Options{}))
	must(Write(path, []byte("good-2"), Options{}))
	if bak, err := os.ReadFile(path + BackupSuffix); err != nil || string(bak) != "good-2" {
		t.Fatalf("backup = %q %v, want the last acknowledged save", bak, err)
	}
	if prev, err := os.ReadFile(path + PreviousSuffix); err != nil || string(prev) != "good-1" {
		t.Fatalf("previous generation = %q %v", prev, err)
	}

	validate := func(b []byte) error {
		if strings.ContainsRune(string(b), 0) {
			return errors.New("contains NUL")
		}
		return nil
	}
	// Torn primary after the acknowledged save.
	must(os.WriteFile(path, []byte("good-2\x00\x00\x00garbage"), 0o600))
	got, recovered, err := Read(path, validate)
	if err != nil || !recovered || string(got) != "good-2" {
		t.Fatalf("recovery: %q %v %v (an acknowledged save was lost)", got, recovered, err)
	}

	// Missing primary, present backup: recovered too.
	must(os.Remove(path))
	got, recovered, err = Read(path, validate)
	if err != nil || !recovered || string(got) != "good-2" {
		t.Fatalf("recovery after loss: %q %v %v", got, recovered, err)
	}

	// Primary and .bak bad: the previous generation still serves.
	must(os.WriteFile(path, []byte("\x00"), 0o600))
	must(os.WriteFile(path+BackupSuffix, []byte("\x00"), 0o600))
	got, recovered, err = Read(path, validate)
	if err != nil || !recovered || string(got) != "good-1" {
		t.Fatalf("recovery from previous generation: %q %v %v", got, recovered, err)
	}

	// Every copy bad: the error names the backups.
	must(os.WriteFile(path+PreviousSuffix, []byte("\x00"), 0o600))
	if _, _, err = Read(path, validate); err == nil || !strings.Contains(err.Error(), "backup also invalid") {
		t.Fatalf("all invalid: %v", err)
	}

	// Nothing exists: fs.ErrNotExist.
	must(os.Remove(path))
	must(os.Remove(path + BackupSuffix))
	must(os.Remove(path + PreviousSuffix))
	if _, _, err = Read(path, validate); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

// TestCrashAtEveryStepLeavesAComplete generation simulates power loss after
// each step of Write: whatever is on disk must read back as either the
// previous save (v1, acknowledged) or the new one (v2); a corrupted primary
// afterwards must still recover a complete generation; and once Write has
// returned, recovery yields v2 and nothing older.
func TestCrashAtEveryStepLeavesACompleteGeneration(t *testing.T) {
	validate := func(b []byte) error {
		if s := string(b); s != "v1" && s != "v2" {
			return errors.New("torn: " + s)
		}
		return nil
	}
	for _, crashAt := range []string{stepTempWritten, stepRenamed, stepRotated, stepBackup, "none"} {
		t.Run(crashAt, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "s")
			if err := Write(path, []byte("v1"), Options{}); err != nil {
				t.Fatal(err)
			}
			crashed := errors.New("power lost")
			stepHook = func(step string) error {
				if step == crashAt {
					return crashed
				}
				return nil
			}
			t.Cleanup(func() { stepHook = nil })
			err := Write(path, []byte("v2"), Options{})
			acknowledged := err == nil
			if crashAt != "none" && !errors.Is(err, crashed) {
				t.Fatalf("crash not simulated: %v", err)
			}
			stepHook = nil

			got, _, rerr := Read(path, validate)
			if rerr != nil {
				t.Fatalf("after crash: %v", rerr)
			}
			if acknowledged && string(got) != "v2" {
				t.Fatalf("acknowledged save lost: read %q", got)
			}
			if string(got) != "v1" && string(got) != "v2" {
				t.Fatalf("read a torn generation %q", got)
			}
			// The primary is then corrupted (torn write after the crash).
			if err := os.WriteFile(path, []byte("\x00\x00"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, recovered, rerr := Read(path, validate)
			if rerr != nil || !recovered {
				t.Fatalf("recovery after crash at %s: %q %v %v", crashAt, got, recovered, rerr)
			}
			if acknowledged && string(got) != "v2" {
				t.Fatalf("acknowledged save not recoverable after crash at %s: read %q", crashAt, got)
			}
			for _, e := range mustReadDir(t, dir) {
				if strings.Contains(e, ".tmp-") && acknowledged {
					t.Fatalf("temp file left behind after a completed write: %s", e)
				}
			}
		})
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestNoBackupOption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s")
	for _, v := range []string{"a", "b"} {
		if err := Write(path, []byte(v), Options{NoBackup: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + BackupSuffix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("NoBackup must not create .bak")
	}
	if _, err := os.Stat(path + PreviousSuffix); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("NoBackup must not create .bak.1")
	}
}

func TestNoTempFilesLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s")
	for i := 0; i < 5; i++ {
		if err := Write(path, []byte{byte(i)}, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	entries := mustReadDir(t, dir)
	for _, e := range entries {
		if strings.Contains(e, ".tmp-") {
			t.Fatalf("temp file left behind: %s", e)
		}
	}
	if len(entries) != 3 {
		t.Fatalf("want file + .bak + .bak.1, got %v", entries)
	}
	bak, _ := os.ReadFile(path + BackupSuffix)
	prev, _ := os.ReadFile(path + PreviousSuffix)
	if string(bak) != "\x04" || string(prev) != "\x03" {
		t.Fatalf("backups = %q %q", bak, prev)
	}
}

func TestWriteIntoUnwritableDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := Write(filepath.Join(ro, "s"), []byte("x"), Options{}); err == nil {
		t.Fatal("expected error")
	}
}
