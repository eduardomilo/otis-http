package collection

import (
	"fmt"
	"strings"
	"unicode"
)

// Slug turns a name a person typed into a file-safe ASCII one: lower-cased,
// with runs of anything other than a letter or digit becoming one hyphen and
// non-ASCII letters dropped (docs/FORMAT.md §7).
//
// The rules are the Postman importer's, and they are shared on purpose: a
// request created in the window and the same request imported from Postman
// should land on the same file name, or the two halves of the product disagree
// about what a collection looks like.
//
// The result can be empty — "***" has nothing in it to keep — which is what
// UniqueName's fallback is for.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// UniqueName resolves a slug against names already in use, appending -2, -3,
// and so on, and substitutes fallback for a slug that is empty or reserved.
//
// `env` is reserved because env/ at the collection root is not part of the
// request tree (docs/FORMAT.md §2.1, §4.3): a folder called that would be
// read as the environment directory.
//
// It records what it hands out in `used`, so a caller naming several things in
// one pass does not have to.
func UniqueName(used map[string]bool, slug, fallback string) string {
	if slug == "" || slug == EnvDirName || strings.TrimLeft(slug, "-") == "" {
		slug = fallback
	}
	candidate := slug
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", slug, i)
	}
	used[candidate] = true
	return candidate
}
