// Package collection turns a directory of .http files into a tree.
//
// The on-disk layout is specified in docs/FORMAT.md §2. In short: every
// directory is a folder, every *.http file except _folder.http is a request,
// _folder.http holds settings that cascade to the folder's requests, and an
// optional .order file fixes the display order of a folder's children.
//
// Loading never modifies the directory. In particular .order is never
// written here; only an explicit reorder (a later increment) may do that.
package collection

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otis-http/otis/internal/httpfile"
)

const (
	// RequestExt is the extension of request files.
	RequestExt = ".http"
	// FolderFileName holds a folder's shared settings.
	FolderFileName = "_folder.http"
	// EnvDirName is the environments directory at the collection root. It
	// belongs to the resolver (Increment 4) and is not part of the tree.
	EnvDirName = "env"
	// ScriptExt is the extension of script files (docs/FORMAT.md §2.4).
	ScriptExt = ".js"
	// PreHookName and PostHookName are a folder's automatic hooks.
	PreHookName  = "_pre" + ScriptExt
	PostHookName = "_post" + ScriptExt
	// PreHookSuffix and PostHookSuffix mark a script belonging to one
	// request: "create-order.pre.js" runs around "create-order.http".
	PreHookSuffix  = ".pre" + ScriptExt
	PostHookSuffix = ".post" + ScriptExt
)

// Kind distinguishes folders from requests.
type Kind string

const (
	KindFolder  Kind = "folder"
	KindRequest Kind = "request"
	// KindScript is a *.js file. It is a tree row because it is a file in the
	// collection that changes what requests do, and a file that changes
	// behaviour while staying invisible is the thing this product exists to
	// argue against.
	KindScript Kind = "script"
)

// Node is a folder or a request in the collection tree.
type Node struct {
	// ID is the path relative to the collection root, "/"-separated, with
	// no trailing slash. It is stable across reloads. The root has ID "".
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Name is the display name: for requests the @name directive, then the
	// "###" title, then the file name without extension; for folders the
	// directory name.
	Name string `json:"name"`
	// Method is the HTTP method label of a request node ("" if broken or
	// if the file has no request line).
	Method string `json:"method,omitempty"`
	// Hook is set on a KindScript node that runs automatically — a folder's
	// _pre.js or _post.js, or a request's <name>.pre.js or <name>.post.js.
	// A script that is not a hook is a plain ES module: nothing runs it
	// unless a hook imports it (docs/FORMAT.md §2.4).
	Hook bool `json:"hook,omitempty"`
	// HookOf is the node ID of the request a request-level hook belongs to,
	// or "" for a folder hook and for a module.
	HookOf string `json:"hookOf,omitempty"`
	// Ordered is set on a folder whose .order file gives it a manual order
	// (docs/FORMAT.md §2.2). The sidebar draws a glyph for it and the folder's
	// menu offers Alphabetical instead of Manual.
	Ordered bool `json:"ordered,omitempty"`
	// Broken is set when the file failed to parse. The node still appears.
	Broken bool   `json:"broken,omitempty"`
	Error  string `json:"error,omitempty"`

	// Children of a folder, in display order.
	Children []*Node `json:"children,omitempty"`

	// Path is the absolute path of the file or directory.
	Path string `json:"-"`
	// Parent is nil for the root.
	Parent *Node `json:"-"`
	// File is the parsed request file (nil when Broken).
	File *httpfile.File `json:"-"`
	// Request is the first entry with a request line in File, or nil.
	Request *httpfile.Request `json:"-"`
	// Settings is the parsed _folder.http of a folder, or nil if absent or
	// unparseable.
	Settings *httpfile.File `json:"-"`
	// SettingsPath is the absolute path of _folder.http when it exists,
	// even if it failed to parse.
	SettingsPath string `json:"-"`
}

// Warning is a non-fatal problem found while loading.
type Warning struct {
	// Path is relative to the collection root, "/"-separated.
	Path string      `json:"path"`
	Code WarningCode `json:"code"`
	Msg  string      `json:"msg"`
}

func (w Warning) String() string { return fmt.Sprintf("%s: %s: %s", w.Path, w.Code, w.Msg) }

// WarningCode identifies the kind of warning.
type WarningCode string

const (
	WarnParseError       WarningCode = "parse-error"        // file failed to parse; node marked broken
	WarnMultipleRequests WarningCode = "multiple-requests"  // more than one request in a file
	WarnNoRequestLine    WarningCode = "no-request-line"    // request file without a request line
	WarnFolderHasRequest WarningCode = "folder-has-request" // _folder.http contains a request line
	WarnOrderMissing     WarningCode = "order-missing"      // .order names an entry that does not exist
	WarnOrderDuplicate   WarningCode = "order-duplicate"    // .order repeats an entry
	WarnUnreadable       WarningCode = "unreadable"         // directory or file could not be read
)

// Collection is a loaded tree plus the warnings raised while loading it.
type Collection struct {
	// Dir is the absolute path of the collection root.
	Dir      string    `json:"-"`
	Root     *Node     `json:"root"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// Load reads the collection rooted at dir.
func Load(dir string) (*Collection, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}
	c := &Collection{Dir: abs}
	c.Root = &Node{ID: "", Kind: KindFolder, Name: filepath.Base(abs), Path: abs}
	c.loadFolder(c.Root)
	return c, nil
}

// DisplayName is the name the UI shows for a collection rooted at dir.
//
// It is the directory's base name, except for a dot-directory, where it is the
// parent's name: a collection kept beside the code it exercises is
// conventionally ".requests", and calling every such collection ".requests"
// would be useless. The design's example is exactly this case — the collection
// named "acme-api" is rooted at "~/code/acme-api/.requests".
//
// This is also the collection component of a secret's key (docs/FORMAT.md §5,
// resolve.CollectionKey). Using the raw base name there would key every
// ".requests" collection on the machine identically, so two projects would
// share one keychain entry per environment and variable.
func DisplayName(dir string) string {
	base := filepath.Base(dir)
	if !strings.HasPrefix(base, ".") {
		return base
	}
	parent := filepath.Base(filepath.Dir(dir))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return base
	}
	return parent
}

// Find returns the node with the given ID, or nil.
func (c *Collection) Find(id string) *Node {
	var found *Node
	c.Walk(func(n *Node) bool {
		if n.ID == id {
			found = n
			return false
		}
		return true
	})
	return found
}

// Walk visits every node depth-first in display order. Returning false from
// fn stops the walk.
func (c *Collection) Walk(fn func(*Node) bool) { walk(c.Root, fn) }

func walk(n *Node, fn func(*Node) bool) bool {
	if !fn(n) {
		return false
	}
	for _, ch := range n.Children {
		if !walk(ch, fn) {
			return false
		}
	}
	return true
}

// Requests returns every request node in display order.
func (c *Collection) Requests() []*Node {
	var out []*Node
	c.Walk(func(n *Node) bool {
		if n.Kind == KindRequest {
			out = append(out, n)
		}
		return true
	})
	return out
}

// Ancestors returns the folders from n's parent up to the root.
func (n *Node) Ancestors() []*Node {
	var out []*Node
	for p := n.Parent; p != nil; p = p.Parent {
		out = append(out, p)
	}
	return out
}

func (c *Collection) warn(rel string, code WarningCode, format string, args ...any) {
	c.Warnings = append(c.Warnings, Warning{Path: rel, Code: code, Msg: fmt.Sprintf(format, args...)})
}

func (c *Collection) loadFolder(folder *Node) {
	entries, err := os.ReadDir(folder.Path)
	if err != nil {
		c.warn(folder.ID, WarnUnreadable, "%v", err)
		return
	}
	var listing []dirEntry
	for _, e := range entries {
		name := e.Name()
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(folder.Path, name)); err == nil {
				isDir = st.IsDir()
			}
		}
		switch {
		case strings.HasPrefix(name, "."):
			continue // hidden; .order is read separately
		case isDir && folder.Parent == nil && name == EnvDirName:
			continue // environments live outside the tree
		case isDir:
			listing = append(listing, dirEntry{name: name, isDir: true})
		case name == FolderFileName:
			c.loadFolderSettings(folder)
		case strings.HasSuffix(name, RequestExt):
			listing = append(listing, dirEntry{name: name})
		case strings.HasSuffix(name, ScriptExt):
			listing = append(listing, dirEntry{name: name})
		}
	}
	// Directory listings are already sorted by name, but make the
	// pre-order input deterministic regardless of the filesystem.
	sort.Slice(listing, func(i, j int) bool { return lessName(listing[i].name, listing[j].name) })

	order, err := ReadOrderFile(filepath.Join(folder.Path, OrderFileName))
	orderRel := path.Join(folder.ID, OrderFileName)
	if err != nil {
		c.warn(orderRel, WarnUnreadable, "%v", err)
	}
	res := applyOrder(listing, order)
	// A folder with a usable .order is drawn with a list glyph (screen 2a) and
	// offers Manual rather than Alphabetical in its menu. A file that exists
	// but lists nothing usable is not a manual order.
	folder.Ordered = res.matched > 0
	for _, m := range res.missing {
		c.warn(orderRel, WarnOrderMissing, "line %d: %q does not exist", m.Line, m.Name)
	}
	for _, d := range res.duplicates {
		c.warn(orderRel, WarnOrderDuplicate, "line %d: %q is listed more than once", d.Line, d.Name)
	}

	for _, e := range res.entries {
		child := &Node{
			ID:     path.Join(folder.ID, e.name),
			Name:   e.name,
			Path:   filepath.Join(folder.Path, e.name),
			Parent: folder,
		}
		switch {
		case e.isDir:
			child.Kind = KindFolder
			c.loadFolder(child)
		case strings.HasSuffix(e.name, ScriptExt):
			child.Kind = KindScript
			child.Hook, child.HookOf = classifyScript(folder, e.name)
		default:
			child.Kind = KindRequest
			c.loadRequest(child)
		}
		folder.Children = append(folder.Children, child)
	}
}

// classifyScript decides what kind of script a file name is: a folder hook, a
// request hook, or a plain module (docs/FORMAT.md §2.4).
//
// A request hook has to name a request that is actually there. "utils.pre.js"
// beside no "utils.http" is a module with an unfortunate name, and calling it
// a hook would tell the reader it runs when nothing will ever run it.
func classifyScript(folder *Node, name string) (hook bool, hookOf string) {
	if name == PreHookName || name == PostHookName {
		return true, ""
	}
	for _, suffix := range []string{PreHookSuffix, PostHookSuffix} {
		base, ok := strings.CutSuffix(name, suffix)
		if !ok {
			continue
		}
		request := base + RequestExt
		if _, err := os.Stat(filepath.Join(folder.Path, request)); err == nil {
			return true, path.Join(folder.ID, request)
		}
	}
	return false, ""
}

func (c *Collection) loadFolderSettings(folder *Node) {
	folder.SettingsPath = filepath.Join(folder.Path, FolderFileName)
	rel := path.Join(folder.ID, FolderFileName)
	f, err := parseRel(folder.SettingsPath)
	if err != nil {
		c.warn(rel, WarnParseError, "%v", err)
		return
	}
	folder.Settings = f
	for _, r := range f.Requests {
		if r.HasRequestLine() {
			c.warn(rel, WarnFolderHasRequest, "line %d: %s %s ignored; folder files hold settings only", r.Line, r.Method, r.URL)
		}
	}
}

func (c *Collection) loadRequest(n *Node) {
	n.Name = strings.TrimSuffix(filepath.Base(n.Path), RequestExt)
	f, err := parseRel(n.Path)
	if err != nil {
		n.Broken = true
		n.Error = err.Error()
		c.warn(n.ID, WarnParseError, "%v", err)
		return
	}
	n.File = f
	var requests []*httpfile.Request
	for _, r := range f.Requests {
		if r.HasRequestLine() {
			requests = append(requests, r)
		}
	}
	switch len(requests) {
	case 0:
		c.warn(n.ID, WarnNoRequestLine, "file contains no request")
		return
	case 1:
	default:
		c.warn(n.ID, WarnMultipleRequests, "file contains %d requests; only the first is used", len(requests))
	}
	n.Request = requests[0]
	n.Method = n.Request.Method
	if name := n.Request.Name(); name != "" {
		n.Name = name
	}
}

// parseRel parses a file; errors carry the base name rather than the full
// path so that messages are stable across machines.
func parseRel(abs string) (*httpfile.File, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	f, err := httpfile.ParseString(string(data))
	if err != nil {
		return nil, err
	}
	return f, nil
}
