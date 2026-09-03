package collection

import (
	"os"
	"path/filepath"
)

// FindRoot locates the collection root at or above dir (docs/FORMAT.md §8).
//
// A directory holding an env/ directory is the root. Otherwise the root is
// the highest ancestor still reachable through directories that carry a
// collection marker (_folder.http or .order); a directory holding .git is
// never crossed.
//
// It is here rather than in the CLI because both halves of the binary need
// the same answer: `otis run path/to/request.http` finds the root so that
// inheritance and env/ work from anywhere, and a .http file double-clicked in
// Finder or Explorer has to resolve to the same collection the CLI would have
// picked. Two implementations of this would mean a file that opens at one root
// from the terminal and another from the desktop.
func FindRoot(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	root := dir
	for {
		if isDir(filepath.Join(dir, EnvDirName)) {
			return dir
		}
		if isDir(filepath.Join(dir, ".git")) {
			return root
		}
		parent := filepath.Dir(dir)
		if parent == dir || !hasMarker(parent) {
			return root
		}
		dir = parent
		root = dir
	}
}

// hasMarker reports whether dir looks like part of a collection, which is
// what lets the walk in FindRoot cross into it.
//
// env/ counts, and has to: a collection root commonly carries nothing but
// env/ and its folders — the design's own `acme-api/.requests` is exactly
// that — and without this the walk stops at the request's own folder and the
// root holding env/ is never even tested. `otis run -e staging` then reports
// the environment as missing while looking at it from one level down.
func hasMarker(dir string) bool {
	for _, name := range []string{FolderFileName, OrderFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return isDir(filepath.Join(dir, EnvDirName))
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
