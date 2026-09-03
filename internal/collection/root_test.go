package collection

import (
	"path/filepath"
	"testing"
)

func TestFindRoot(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"repo/.git/HEAD":              "ref: x\n",
		"repo/api/env/dev.json":       "{}",
		"repo/api/_folder.http":       "@a = 1\n",
		"repo/api/users/_folder.http": "@b = 2\n",
		"repo/api/users/create.http":  "GET https://x.test\n",
		"repo/other/loose.http":       "GET https://x.test\n",
		"standalone/a/b/.order":       "c.http\n",
		"standalone/a/b/c.http":       "GET https://x.test\n",
		"standalone/a/_folder.http":   "@x = 1\n",
		// The design's own layout: a root carrying env/ and folders, with no
		// _folder.http or .order of its own. The walk has to be able to cross
		// into it or `otis run -e staging` reports the environment as missing
		// from one level down.
		"bare/.requests/env/staging.json":    "{}",
		"bare/.requests/orders/_folder.http": "Accept: application/json\n",
		"bare/.requests/orders/o.http":       "GET https://x.test\n",
	})
	tests := map[string]string{
		"repo/api/users":        "repo/api",   // stops at env/
		"repo/api":              "repo/api",   // env/ here
		"repo/other":            "repo/other", // no markers above
		"standalone/a/b":        "standalone/a",
		"bare/.requests/orders": "bare/.requests", // env/ is a marker too
	}
	for start, want := range tests {
		got := FindRoot(filepath.Join(dir, filepath.FromSlash(start)))
		if got != filepath.Join(dir, filepath.FromSlash(want)) {
			t.Errorf("FindRoot(%s) = %s, want %s", start, got, filepath.Join(dir, want))
		}
	}
}
