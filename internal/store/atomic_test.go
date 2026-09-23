package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// assertOnlyEntries fails unless dir holds exactly the named entries — no temp
// file left behind by a write.
func assertOnlyEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	if len(got) != len(names) {
		t.Fatalf("%s holds %v, want exactly %v", dir, got, names)
	}
	for i := range names {
		if got[i] != names[i] {
			t.Fatalf("%s holds %v, want exactly %v", dir, got, names)
		}
	}
}

func TestWriteFileAtomic_CreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")

	if err := WriteFileAtomic(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic (create): %v", err)
	}
	assertFile(t, path, "v1\n")
	if err := WriteFileAtomic(path, []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic (replace): %v", err)
	}
	assertFile(t, path, "v2\n")
	assertOnlyEntries(t, dir, "state.toml")

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("mode = %v, want 0644 (not the temp file's 0600)", got)
		}
	}
}

// A write that fails part-way leaves the old file intact and no temp file —
// never a truncated or half-written state.toml.
func TestWriteFileAtomic_FailureLeavesOldContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	mustWrite(t, path, "old content\n", 0o644)

	boom := errors.New("disk full")
	orig := writeTemp
	writeTemp = func(f *os.File, data []byte) error {
		_, _ = f.Write(data[:len(data)/2])
		return boom
	}
	t.Cleanup(func() { writeTemp = orig })

	err := WriteFileAtomic(path, []byte("new content that never fully lands\n"), 0o644)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the simulated failure", err)
	}
	assertFile(t, path, "old content\n")
	assertOnlyEntries(t, dir, "state.toml")

	// With no previous file, a failed write creates nothing.
	fresh := filepath.Join(dir, "config.toml")
	if err := WriteFileAtomic(fresh, []byte("x"), 0o644); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the simulated failure", err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("failed write left %s behind: err = %v", fresh, err)
	}
	assertOnlyEntries(t, dir, "state.toml")
}
