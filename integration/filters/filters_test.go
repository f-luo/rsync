// Package filters_test exercises --exclude, --include, --exclude-from,
// and --include-from end-to-end through the gokrazy client/daemon
// pair. The tests speak to behavior, not wire bytes: "given these
// filter flags, what lands on disk?". Wire-format roundtrips are
// unit-tested in internal/sender.
package filters_test

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gokrazy/rsync/internal/rsynctest"
)

func TestMain(m *testing.M) {
	if err := rsynctest.CommandMain(m); err != nil {
		log.Fatal(err)
	}
}

// fixture creates a source tree with a mix of names designed to
// exercise every filter-rule shape: basename globs (*.log), anchored
// rules (/top), dir-only rules (build/), and nested paths that let
// us tell apart "file skipped" from "directory pruned".
func fixture(t *testing.T, root string) {
	t.Helper()
	entries := map[string]string{
		"keep.txt":               "keep",
		"drop.log":               "drop",
		"nested/keep.go":         "ok",
		"nested/drop.log":        "drop",
		"nested/deeper/keep.md":  "ok",
		"nested/deeper/drop.log": "drop",
		"build/out.bin":          "build",
		"build/nested/out.bin":   "build",
		"src/a.go":               "src",
		"src/a.tmp":              "tmp",
		"top-only":               "anchored",
		"sub/top-only":           "unanchored copy",
	}
	for rel, content := range entries {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// list walks dst and returns all regular-file relative paths, sorted.
// Directories are elided so assertions read "these files made it".
func list(t *testing.T, dst string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dst, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func equalSet(got, want []string) string {
	if len(got) != len(want) {
		return diffLines(got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			return diffLines(got, want)
		}
	}
	return ""
}

func diffLines(got, want []string) string {
	return "got:\n  " + strings.Join(got, "\n  ") +
		"\nwant:\n  " + strings.Join(want, "\n  ")
}

// TestDownloadExcludeBasenameGlob exercises the client-as-receiver /
// server-as-sender path: the client pushes `- *.log` to the server
// over the wire; the server must skip those files in its walk.
func TestDownloadExcludeBasenameGlob(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	fixture(t, src)

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--exclude", "*.log",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	want := []string{
		"build/nested/out.bin",
		"build/out.bin",
		"keep.txt",
		"nested/deeper/keep.md",
		"nested/keep.go",
		"src/a.go",
		"src/a.tmp",
		"sub/top-only",
		"top-only",
	}
	if diff := equalSet(got, want); diff != "" {
		t.Fatalf("--exclude '*.log' download mismatch:\n%s", diff)
	}
}

// TestDownloadExcludeDirectoryOnly exercises rsync's dir-only rule
// semantics: `- build/` must prune the whole build/ subtree (both
// nested files), not merely skip files named "build".
func TestDownloadExcludeDirectoryOnly(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	fixture(t, src)

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--exclude", "build/",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	// build/ should be entirely gone; everything else should land.
	want := []string{
		"drop.log",
		"keep.txt",
		"nested/deeper/drop.log",
		"nested/deeper/keep.md",
		"nested/drop.log",
		"nested/keep.go",
		"src/a.go",
		"src/a.tmp",
		"sub/top-only",
		"top-only",
	}
	if diff := equalSet(got, want); diff != "" {
		t.Fatalf("--exclude 'build/' download mismatch:\n%s", diff)
	}
}

// TestDownloadAnchoredRule exercises a leading-slash anchor: `- /top-only`
// must match the top-level entry but leave sub/top-only alone.
func TestDownloadAnchoredRule(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	fixture(t, src)

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--exclude", "/top-only",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	// Anchored rule drops ./top-only but NOT sub/top-only.
	if slices.Contains(got, "top-only") {
		t.Errorf("anchored rule failed to exclude top-only; got: %v", got)
	}
	if !slices.Contains(got, "sub/top-only") {
		t.Errorf("anchored rule over-matched; sub/top-only should remain; got: %v", got)
	}
}

// TestDownloadIncludeBeforeExclude exercises first-match-wins: an
// include-before-exclude rule should rescue keep.go from `- *.go`.
func TestDownloadIncludeBeforeExclude(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	fixture(t, src)

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--include", "*.go",
		"--exclude", "*.tmp",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	// *.go files should transfer (include wins first); *.tmp should drop.
	for _, p := range got {
		if strings.HasSuffix(p, ".tmp") {
			t.Errorf("--exclude '*.tmp' failed to drop %s; got: %v", p, got)
		}
	}
	foundGo := false
	for _, p := range got {
		if p == "src/a.go" || p == "nested/keep.go" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Errorf("--include '*.go' didn't preserve any .go files; got: %v", got)
	}
}

// TestDownloadExcludeFromFile exercises --exclude-from: rules read
// from a file must behave like the same rules on the command line,
// including comment-skipping and the leading # handling.
func TestDownloadExcludeFromFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	rules := filepath.Join(tmp, "rules.txt")
	fixture(t, src)

	if err := os.WriteFile(rules, []byte(`# skip noise
*.log

# and this one
build/
`), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--exclude-from", rules,
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	for _, p := range got {
		if strings.HasSuffix(p, ".log") {
			t.Errorf("--exclude-from didn't drop %s; got: %v", p, got)
		}
		if strings.HasPrefix(p, "build/") {
			t.Errorf("--exclude-from didn't prune build/; got: %v", p)
		}
	}
}

// TestUploadLocalFilter exercises the client-as-sender path: the
// client applies --exclude locally to its own walk, so the server
// should only receive non-filtered files.
func TestUploadLocalFilter(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	fixture(t, src)

	srv := rsynctest.New(t, rsynctest.WritableInteropModule(dst))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--exclude", "*.log",
		"--exclude", "build/",
		src+"/",
		"rsync://localhost:"+srv.Port+"/interop/",
	)

	got := list(t, dst)
	for _, p := range got {
		if strings.HasSuffix(p, ".log") || strings.HasPrefix(p, "build/") {
			t.Errorf("upload didn't apply filter locally; got: %v", got)
		}
	}
}

// TestDeleteProtectsExcluded is the headline --delete interaction:
// files on the destination that match a filter-exclude rule must not
// be removed, even though they are not in the sender's file list.
// This is rsync's "protection" of excluded paths during --delete.
//
// Runs as pull (client-as-receiver), which is the flow that honors
// --delete in gokrazy today; the push-side server-arg forwarding
// for --delete is commented out in rsyncopts/serveroptions.go.
func TestDeleteProtectsExcluded(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")

	// Source has only keep.txt; destination starts with keep.txt
	// plus an extra drop.log that IS filter-excluded and an extra
	// other.txt that is NOT excluded. --delete should remove
	// other.txt but leave drop.log alone (protected).
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"keep.txt":  "stale",
		"drop.log":  "protected",
		"other.txt": "deletable",
	} {
		if err := os.WriteFile(filepath.Join(dst, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a", "--delete",
		"--exclude", "*.log",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	want := []string{"drop.log", "keep.txt"}
	if diff := equalSet(got, want); diff != "" {
		t.Fatalf("--delete filter protection mismatch:\n%s", diff)
	}
}

// TestNoFilterFlagsPreservesBaseline is a regression guard: invoking
// the client without any filter flags must transfer the entire tree,
// byte-for-byte identical to the pre-filter-support behavior. This
// catches regressions where Send/Recv of an empty list accidentally
// drops something.
func TestNoFilterFlagsPreservesBaseline(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src"), filepath.Join(tmp, "dst")
	fixture(t, src)

	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)

	got := list(t, dst)
	want := []string{
		"build/nested/out.bin",
		"build/out.bin",
		"drop.log",
		"keep.txt",
		"nested/deeper/drop.log",
		"nested/deeper/keep.md",
		"nested/drop.log",
		"nested/keep.go",
		"src/a.go",
		"src/a.tmp",
		"sub/top-only",
		"top-only",
	}
	if diff := equalSet(got, want); diff != "" {
		t.Fatalf("no-filter baseline drifted:\n%s", diff)
	}
}
