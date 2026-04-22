// Package filters_test exercises --exclude, --include, --exclude-from,
// and --include-from end-to-end through the gokrazy client/daemon pair.
// Behavior under test: "given these flags, what lands on disk?". Wire
// roundtrips are unit-tested in internal/sender.
package filters_test

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gokrazy/rsync/internal/rsynctest"
)

func TestMain(m *testing.M) {
	if err := rsynctest.CommandMain(m); err != nil {
		log.Fatal(err)
	}
}

// fixtureFiles covers every filter-rule shape: basename globs (*.log),
// anchored rules (/top-only vs sub/top-only), dir-only rules (build/),
// and nested paths that distinguish "file skipped" from "dir pruned".
var fixtureFiles = map[string]string{
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

var allFixtureFiles = []string{
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

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func list(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func assertFiles(t *testing.T, got, want []string) {
	t.Helper()
	if slices.Equal(got, want) {
		return
	}
	t.Fatalf("files mismatch\ngot:\n  %s\nwant:\n  %s",
		strings.Join(got, "\n  "), strings.Join(want, "\n  "))
}

// pullInto serves fixtureFiles over rsyncd and pulls into a fresh dst
// with the given extra client flags. Returns dst for inspection.
func pullInto(t *testing.T, flags ...string) string {
	t.Helper()
	src := t.TempDir()
	writeTree(t, src, fixtureFiles)
	dst := t.TempDir()
	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	args := append([]string{"gokr-rsync", "-a"}, flags...)
	args = append(args, "rsync://localhost:"+srv.Port+"/interop/", dst)
	rsynctest.Run(t, args...)
	return dst
}

// TestDownloadFilters exercises the client-as-receiver / server-as-
// sender path: the client sends filter rules on the wire; the server
// applies them during its walk. Each case pins the full destination
// tree so a regression shows up as a readable diff.
func TestDownloadFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags []string
		want  []string
	}{
		{
			name: "no-flags-baseline",
			want: allFixtureFiles,
		},
		{
			name:  "exclude-basename-glob",
			flags: []string{"--exclude", "*.log"},
			want: []string{
				"build/nested/out.bin",
				"build/out.bin",
				"keep.txt",
				"nested/deeper/keep.md",
				"nested/keep.go",
				"src/a.go",
				"src/a.tmp",
				"sub/top-only",
				"top-only",
			},
		},
		{
			// Dir-only rule must prune the whole subtree, not merely
			// skip files named "build".
			name:  "exclude-directory-only",
			flags: []string{"--exclude", "build/"},
			want: []string{
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
			},
		},
		{
			// First-match wins: *.go rescued by include before *.tmp
			// exclude; everything else passes through default-include.
			name:  "include-before-exclude",
			flags: []string{"--include", "*.go", "--exclude", "*.tmp"},
			want: []string{
				"build/nested/out.bin",
				"build/out.bin",
				"drop.log",
				"keep.txt",
				"nested/deeper/drop.log",
				"nested/deeper/keep.md",
				"nested/drop.log",
				"nested/keep.go",
				"src/a.go",
				"sub/top-only",
				"top-only",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFiles(t, list(t, pullInto(t, tc.flags...)), tc.want)
		})
	}
}

// TestDownloadAnchoredRule: a leading-slash rule binds to the transfer
// root, so /top-only drops ./top-only but leaves sub/top-only.
func TestDownloadAnchoredRule(t *testing.T) {
	t.Parallel()
	got := list(t, pullInto(t, "--exclude", "/top-only"))
	if slices.Contains(got, "top-only") {
		t.Errorf("anchored rule failed to exclude top-only; got: %v", got)
	}
	if !slices.Contains(got, "sub/top-only") {
		t.Errorf("anchored rule over-matched; sub/top-only missing; got: %v", got)
	}
}

// TestDownloadBroadExcludeStillTransfersRoot pins the transfer-root
// carve-out in sender/flist.go: --exclude '*' still has to create the
// destination directory or the transfer produces nothing.
func TestDownloadBroadExcludeStillTransfersRoot(t *testing.T) {
	t.Parallel()
	dst := pullInto(t, "--exclude", "*")
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("--exclude '*' suppressed the transfer root: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("transfer root is not a directory: %v", info.Mode())
	}
	if got := list(t, dst); len(got) != 0 {
		t.Errorf("--exclude '*' left files: %v", got)
	}
}

// TestDownloadExcludeFromFile: rules read from a file must behave like
// the same rules on the command line, including blank-line and leading-
// '#' comment handling.
func TestDownloadExcludeFromFile(t *testing.T) {
	t.Parallel()
	rules := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(rules, []byte("# skip noise\n*.log\n\n# and this\nbuild/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range list(t, pullInto(t, "--exclude-from", rules)) {
		if strings.HasSuffix(p, ".log") || strings.HasPrefix(p, "build/") {
			t.Errorf("--exclude-from leaked %s", p)
		}
	}
}

// TestUploadLocalFilter exercises the client-as-sender path: filters
// apply to the client's own walk, so only the filtered set reaches the
// daemon.
func TestUploadLocalFilter(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeTree(t, src, fixtureFiles)
	dst := t.TempDir()
	srv := rsynctest.New(t, rsynctest.WritableInteropModule(dst))
	rsynctest.Run(t,
		"gokr-rsync", "-a",
		"--exclude", "*.log",
		"--exclude", "build/",
		src+"/",
		"rsync://localhost:"+srv.Port+"/interop/",
	)
	for _, p := range list(t, dst) {
		if strings.HasSuffix(p, ".log") || strings.HasPrefix(p, "build/") {
			t.Errorf("upload leaked %s", p)
		}
	}
}

// TestDeleteProtectsExcluded: files on the destination matching a
// filter-exclude rule must survive --delete even though the sender's
// file list omits them. Runs as pull because push-side --delete arg
// forwarding is currently disabled in rsyncopts/serveroptions.go.
func TestDeleteProtectsExcluded(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeTree(t, src, map[string]string{"keep.txt": "keep"})
	dst := t.TempDir()
	writeTree(t, dst, map[string]string{
		"keep.txt":  "stale",
		"drop.log":  "protected",
		"other.txt": "deletable",
	})
	srv := rsynctest.New(t, rsynctest.InteropModule(src))
	rsynctest.Run(t,
		"gokr-rsync", "-a", "--delete",
		"--exclude", "*.log",
		"rsync://localhost:"+srv.Port+"/interop/",
		dst,
	)
	assertFiles(t, list(t, dst), []string{"drop.log", "keep.txt"})
}
