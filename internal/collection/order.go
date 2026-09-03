package collection

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OrderFileName is the per-directory ordering file. See docs/FORMAT.md §2.2.
const OrderFileName = ".order"

// Order is the parsed content of a .order file: entry names in the order
// they should appear. Directories carry a trailing slash.
type Order struct {
	Entries []OrderEntry
}

// OrderEntry is one non-blank, non-comment line of a .order file.
type OrderEntry struct {
	Name string // as written, trimmed
	Line int
}

// ReadOrderFile parses the .order file at path. A missing file yields an
// empty Order and no error.
func ReadOrderFile(path string) (Order, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Order{}, nil
	}
	if err != nil {
		return Order{}, err
	}
	defer f.Close()
	return ParseOrder(f)
}

// ParseOrder parses .order content: one name per line, blank lines
// ignored, lines starting with "#" are comments.
func ParseOrder(r io.Reader) (Order, error) {
	var o Order
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		o.Entries = append(o.Entries, OrderEntry{Name: text, Line: line})
	}
	return o, sc.Err()
}

// dirEntry is what the sorter sees: a name and whether it is a directory.
type dirEntry struct {
	name  string
	isDir bool
}

// orderKey is the exact .order spelling of an entry.
func (e dirEntry) orderKey() string {
	if e.isDir {
		return e.name + "/"
	}
	return e.name
}

// sortResult is the outcome of applying an Order to a directory listing.
type sortResult struct {
	entries    []dirEntry   // final order
	matched    int          // entries placed by .order, before the alphabetical rest
	missing    []OrderEntry // .order lines that matched nothing
	duplicates []OrderEntry // .order lines repeating an earlier match
}

// applyOrder sorts entries: those matched by o first, in o's order, then
// the rest alphabetically (case-insensitive, byte order as tie-break).
//
// Matching: an .order line matches an entry when it equals the entry's exact
// key ("name.http" or "dir/"). As a convenience a bare line "name" (no slash,
// no .http suffix) matches the file "name.http", or failing that the
// directory "name".
func applyOrder(entries []dirEntry, o Order) sortResult {
	byKey := make(map[string]int, len(entries))
	for i, e := range entries {
		byKey[e.orderKey()] = i
	}
	used := make([]bool, len(entries))
	var res sortResult
	for _, oe := range o.Entries {
		idx, ok := byKey[oe.Name]
		if !ok && !strings.HasSuffix(oe.Name, "/") && !strings.HasSuffix(oe.Name, RequestExt) {
			if idx, ok = byKey[oe.Name+RequestExt]; !ok {
				idx, ok = byKey[oe.Name+"/"]
			}
		}
		switch {
		case !ok:
			res.missing = append(res.missing, oe)
		case used[idx]:
			res.duplicates = append(res.duplicates, oe)
		default:
			used[idx] = true
			res.entries = append(res.entries, entries[idx])
		}
	}
	res.matched = len(res.entries)
	var rest []dirEntry
	for i, e := range entries {
		if !used[i] {
			rest = append(rest, e)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return lessName(rest[i].name, rest[j].name) })
	res.entries = append(res.entries, rest...)
	return res
}

// lessName orders names case-insensitively, falling back to byte order so
// that "A" and "a" have a stable relative position.
func lessName(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

// FormatOrder renders a .order file listing names in order.
//
// One name per line, exact spellings (a directory keeps its trailing slash),
// and a leading comment naming what wrote it — an .order file that appears in
// somebody's diff should say where it came from. Nothing else: no timestamp,
// no version, nothing that would make two reorders of the same order produce
// two different files.
func FormatOrder(names []string) []byte {
	var b strings.Builder
	b.WriteString("# Order maintained by Otis. Drag rows in the sidebar to change it.\n")
	b.WriteString("# Unlisted entries sort alphabetically after these.\n")
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// WriteOrder writes the .order file for dir, listing names in order.
//
// The write is atomic-ish: a temporary file in the same directory, renamed
// into place, so a reader never sees half a list. It is the caller's job to
// hold the write guard.
func WriteOrder(dir string, names []string) error {
	path := filepath.Join(dir, OrderFileName)
	tmp, err := os.CreateTemp(dir, ".order-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(FormatOrder(names)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// RemoveOrder deletes dir's .order file, which is how a folder returns to
// alphabetical order (docs/FORMAT.md §2.2). A missing file is not an error:
// alphabetical is already what a folder without one does.
func RemoveOrder(dir string) error {
	err := os.Remove(filepath.Join(dir, OrderFileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
