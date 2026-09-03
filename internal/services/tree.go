package services

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/git"
)

// NodeKind discriminates the rows in the sidebar tree.
type NodeKind string

const (
	// KindFolder is a directory.
	KindFolder NodeKind = "folder"
	// KindRequest is a *.http file that parsed.
	KindRequest NodeKind = "request"
	// KindFolderFile is a folder's _folder.http. It is not a tree row —
	// docs/FORMAT.md §2.1 is explicit that it is settings, not a request, and
	// `otis ls` does not list it — so it hangs off its folder's Settings
	// field rather than its Children.
	KindFolderFile NodeKind = "folderfile"
	// KindBroken is a *.http file that failed to parse. It is still a row and
	// still opens: seeing the parse error is the point.
	KindBroken NodeKind = "broken"
	// KindHook is a *.js file that runs automatically: a folder's _pre.js or
	// _post.js, or a request's <name>.pre.js or <name>.post.js.
	KindHook NodeKind = "hook"
	// KindModule is any other *.js file — a plain ES module that runs only
	// when a hook imports it (docs/FORMAT.md §2.4).
	//
	// Two kinds rather than one with a flag, because the whole point of the
	// distinction is that the reader can see it: the tree badges them HOOK and
	// LIB, and a row that says neither would leave "does this run?" to the
	// naming convention.
	KindModule NodeKind = "module"
)

// Node is one entry in the tree the sidebar renders.
type Node struct {
	// Path is the node's collection-relative ID, "/"-separated, empty for the
	// root (docs/FORMAT.md §2.1). It is what routes carry and what the
	// expand/collapse and selection state is keyed by.
	Path string   `json:"path"`
	Kind NodeKind `json:"kind"`
	// Name is the display name: a request's @name, then its "###" title, then
	// the file name; a folder's directory name.
	Name string `json:"name"`
	// Method is a request's HTTP method, empty when the file has no request
	// line or failed to parse.
	Method string `json:"method,omitempty"`
	// HookOf is the request a KindHook node runs around, as a node path, or
	// "" for a folder hook.
	HookOf string `json:"hookOf,omitempty"`
	// URL is a request's URL as written, with its `{{references}}` intact.
	//
	// It travels with the tree because the command palette searches it and
	// shows it (screen 2c matches "ord" against `/v2/orders`), and asking Go
	// for it per row would be a binding call per keystroke. As written rather
	// than resolved: the palette has no environment in mind, and a URL that
	// changed as you switched environments would be a moving target to
	// search.
	URL string `json:"url,omitempty"`
	// Ordered is set on a folder that has a `.order` file giving it a manual
	// order (docs/FORMAT.md §2.2). The sidebar draws a list glyph for it
	// (screen 2a) and the folder's menu offers Alphabetical rather than Manual.
	Ordered bool `json:"ordered,omitempty"`
	// GitStatus is "M", "U", "A" or "D", empty when the file is clean or not
	// in a repository.
	GitStatus string `json:"gitStatus,omitempty"`
	// Error is the parse error on a broken node.
	Error string `json:"error,omitempty"`
	// Warnings are the load warnings that belong to this node, already
	// rendered for display.
	Warnings []string `json:"warnings,omitempty"`
	// Settings is a folder's _folder.http, or nil when it has none. Its
	// presence is what marks a folder as carrying shared settings.
	Settings *Node `json:"settings,omitempty"`
	// Children of a folder, in display order (docs/FORMAT.md §2.2).
	Children []Node `json:"children,omitempty"`
	// Modified is set on a folder when any file below it is modified, so the
	// tree can show a dirty dot on a collapsed folder.
	Modified bool `json:"modified,omitempty"`
}

// Tree is a loaded collection and everything the sidebar needs to draw it.
type Tree struct {
	Root Node `json:"root"`
	// Warnings is every load warning, including those already attached to a
	// node, so a future problems view has the whole list in one place.
	Warnings []Warning `json:"warnings"`
	// Git is the repository state at the moment the tree was walked. It
	// travels with the tree because a new file is untracked the instant it
	// appears, and shipping the two separately would show the tree without
	// its dots for a frame.
	Git git.State `json:"git"`
}

// Warning is a load warning, flattened for the frontend.
type Warning struct {
	Path string `json:"path"`
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// buildTree converts a loaded collection and a git state into the tree the
// frontend renders.
func buildTree(loaded *collection.Collection, state git.State) Tree {
	tree := Tree{Git: state}
	byPath := warningsByNode(loaded.Warnings)
	for _, w := range loaded.Warnings {
		tree.Warnings = append(tree.Warnings, Warning{Path: w.Path, Code: string(w.Code), Msg: w.Msg})
	}
	tree.Root = convert(loaded.Root, byPath, state)
	return tree
}

// convert turns one collection node into a display node, recursively.
func convert(n *collection.Node, warnings map[string][]string, state git.State) Node {
	out := Node{
		Path:      n.ID,
		Name:      n.Name,
		Method:    n.Method,
		Warnings:  warnings[n.ID],
		GitStatus: string(state.Statuses[n.ID]),
	}
	switch {
	case n.Kind == collection.KindFolder:
		out.Kind = KindFolder
		out.Ordered = n.Ordered
		if n.SettingsPath != "" {
			settingsPath := path.Join(n.ID, collection.FolderFileName)
			out.Settings = &Node{
				Path:      settingsPath,
				Kind:      KindFolderFile,
				Name:      collection.FolderFileName,
				GitStatus: string(state.Statuses[settingsPath]),
				Warnings:  warnings[settingsPath],
				// A _folder.http that failed to parse leaves Settings nil on
				// the collection node but keeps SettingsPath, which is how a
				// broken one is told from an absent one.
				Error: settingsError(n),
			}
		}
	case n.Kind == collection.KindScript:
		out.Kind = KindModule
		if n.Hook {
			out.Kind = KindHook
			out.HookOf = n.HookOf
		}
	case n.Broken:
		out.Kind = KindBroken
		out.Error = n.Error
	default:
		out.Kind = KindRequest
		if n.Request != nil {
			out.URL = n.Request.URL
		}
	}

	for _, child := range n.Children {
		converted := convert(child, warnings, state)
		out.Modified = out.Modified || converted.Modified || propagates(converted.GitStatus)
		out.Children = append(out.Children, converted)
	}
	if out.Settings != nil && propagates(out.Settings.GitStatus) {
		out.Modified = true
	}
	return out
}

// settingsError reports the parse error of a folder's _folder.http, or "" when
// it parsed. A folder whose settings file is broken keeps SettingsPath but has
// no Settings (docs/FORMAT.md §3.4).
func settingsError(n *collection.Node) string {
	if n.SettingsPath == "" || n.Settings != nil {
		return ""
	}
	return "this file could not be parsed; the folder contributes no settings"
}

// propagates reports whether a status raises the dot on the file's ancestors.
//
// Only a *tracked* change does. Screen 1a is explicit about this: orders/
// carries an amber dot for its modified create-order, while fixtures/ carries
// none even though seed-order inside it is untracked. A new file is visible
// as itself; a change buried inside a collapsed folder is not, which is what
// the folder dot is for. A deleted file cannot be a row at all, so it counts.
func propagates(status string) bool {
	return status == string(git.StatusModified) || status == string(git.StatusDeleted)
}

// warningsByNode indexes warnings by the node they belong to. A warning about
// a folder's .order file (docs/FORMAT.md §2.2) belongs to the folder: .order
// is not a row, and the folder is where the reader will look.
func warningsByNode(warnings []collection.Warning) map[string][]string {
	out := map[string][]string{}
	for _, w := range warnings {
		key := w.Path
		if base := path.Base(key); base == collection.OrderFileName {
			key = path.Dir(key)
			if key == "." {
				key = ""
			}
		}
		out[key] = append(out[key], string(w.Code)+": "+w.Msg)
	}
	for _, list := range out {
		sort.Strings(list)
	}
	return out
}

// Walk visits every node in the tree in display order, including the
// _folder.http hanging off each folder.
func (n *Node) Walk(fn func(*Node)) {
	fn(n)
	if n.Settings != nil {
		fn(n.Settings)
	}
	for i := range n.Children {
		n.Children[i].Walk(fn)
	}
}

// requestCount is the number of request rows below n, used by the tests and
// by the folder view later.
func (n *Node) requestCount() int {
	count := 0
	n.Walk(func(node *Node) {
		if node.Kind == KindRequest || node.Kind == KindBroken {
			count++
		}
	})
	return count
}

// absolutePath joins the collection root and a node path.
//
// Node paths come from the frontend, so they are treated as untrusted: the
// path is cleaned as an absolute slash path first, which collapses any "..",
// and only then joined to the root. Nothing can address a file outside the
// collection.
func absolutePath(root, nodePath string) string {
	clean := strings.TrimPrefix(path.Clean("/"+nodePath), "/")
	if clean == "." || clean == "" {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(clean))
}
