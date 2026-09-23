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

// A symlinked destination (config.toml linked from a dotfiles repo) stays a
// link: the write lands in its target, as os.WriteFile's would.
func TestWriteFileAtomic_WritesThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "skillm.toml")
	mustWrite(t, target, "old\n", 0o644)
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(link, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.toml is no longer a symlink (err = %v)", err)
	}
	assertFile(t, target, "new\n")
	assertOnlyEntries(t, filepath.Dir(target), "skillm.toml")
}

// An existing file keeps its mode; a new one gets perm filtered by the umask,
// as os.WriteFile would give it.
func TestWriteFileAtomic_Modes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()

	private := filepath.Join(dir, "state.toml")
	mustWrite(t, private, "old\n", 0o600)
	if err := os.Chmod(private, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(private, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	assertMode(t, private, 0o600)

	ref := filepath.Join(dir, "ref")
	if err := os.WriteFile(ref, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ref)
	if err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "config.toml")
	if err := WriteFileAtomic(fresh, []byte("x"), 0o666); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	assertMode(t, fresh, info.Mode().Perm())
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %v, want %v", path, got, want)
	}
}
