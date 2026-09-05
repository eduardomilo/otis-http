package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otis-http/otis/internal/collection"
)

// ScriptDocument is one `.js` file as the editor needs it.
//
// A script is a file in the collection and a row in the tree (docs/FORMAT.md
// §2.4), and until this existed it was the only row you could not open:
// clicking one showed the folder that held it. The engine ran it and the
// window could not show it.
type ScriptDocument struct {
	// Path is the node's collection-relative ID.
	Path string `json:"path"`
	// Name is the file name, which is a script's whole name — there is no
	// `@name` directive in JavaScript and inventing a magic comment for one
	// would be inventing syntax (docs/FORMAT.md §9).
	Name string `json:"name"`
	// Folder is the node path of the folder that holds it, "" for the root.
	Folder string `json:"folder"`
	// Text is the file's contents.
	Text string `json:"text"`

	// Hook is true when this script runs on its own, false for a module that
	// runs only when a hook imports it (§2.4).
	Hook bool `json:"hook"`
	// Phase is "pre" or "post" for a hook, "" for a module.
	Phase string `json:"phase,omitempty"`
	// HookOf is the request a request-level hook runs around, as a node path.
	// Empty for a folder hook and for a module.
	HookOf string `json:"hookOf,omitempty"`
	// Scope is what a folder hook runs around: the folder's node path. Empty
	// for a request hook and for a module.
	Scope string `json:"scope,omitempty"`
}

// ScriptService loads and writes the `.js` files in a collection, and is the
// only thing that writes one.
//
// Separate from RequestService for the reason every writer in this package is
// separate: "does anything else write this kind of file?" should be a question
// answered by reading one file. A `.js` is not a `.http` — nothing parses it,
// nothing serializes it, and the round-trip guarantee of docs/FORMAT.md §1.13
// has nothing to say about it. **The text is written verbatim**: Otis has no
// opinion about JavaScript formatting and adding one would put every
// colleague's prettier config in somebody's diff.
type ScriptService struct {
	collections *CollectionService
}

// NewScriptService constructs the service.
func NewScriptService(collections *CollectionService) *ScriptService {
	return &ScriptService{collections: collections}
}

// Load reads the script at nodePath.
func (s *ScriptService) Load(nodePath string) (ScriptDocument, error) {
	loaded, node, err := s.script(nodePath)
	if err != nil {
		return ScriptDocument{}, err
	}
	text, err := os.ReadFile(node.Path)
	if err != nil {
		return ScriptDocument{}, fmt.Errorf("reading %s: %w", displayPath(nodePath), err)
	}
	_ = loaded
	return describeScript(node, string(text)), nil
}

// Save writes text to the script at nodePath and returns it as it now stands.
//
// Verbatim, byte for byte. The window sends what the editor holds and Go
// writes it: there is no serializer in the path because there is nothing to
// serialize, which also means a script cannot be reformatted by saving it.
func (s *ScriptService) Save(nodePath, text string) (ScriptDocument, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return ScriptDocument{}, err
	}
	// The node need not be in the cached walk — a file created a moment ago
	// may not be — but it must be inside the collection and it must be a
	// script. absolutePath cleans first, so nothing the window sends can
	// address a file outside it.
	target := absolutePath(loaded.Dir, nodePath)
	if !strings.HasSuffix(target, collection.ScriptExt) {
		return ScriptDocument{}, fmt.Errorf("%s is not a %s file", displayPath(nodePath), collection.ScriptExt)
	}
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		return ScriptDocument{}, fmt.Errorf("saving %s: %w", displayPath(nodePath), err)
	}

	// Inside the guard, like every write Otis makes.
	release := s.collections.Guard().Writing(target)
	err = writeFileAtomic(target, []byte(text))
	release()
	if err != nil {
		return ScriptDocument{}, fmt.Errorf("saving %s: %w", displayPath(nodePath), err)
	}
	// The guard means the watcher will not announce this, so the write does.
	// A re-walk matters here beyond the tree: whether a `.pre.js` counts as a
	// hook depends on a request file existing beside it (§2.4), and a script
	// saved into a folder changes the folder view's Scripts panel.
	if err := s.collections.Refresh(); err != nil {
		return ScriptDocument{}, err
	}
	return s.Load(nodePath)
}

// script resolves a node path to a script in the cached walk.
func (s *ScriptService) script(nodePath string) (*collection.Collection, *collection.Node, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return nil, nil, err
	}
	node := loaded.Find(nodePath)
	if node == nil {
		return nil, nil, fmt.Errorf("%s is not in the collection", displayPath(nodePath))
	}
	if node.Kind != collection.KindScript {
		return nil, nil, fmt.Errorf("%s is not a script", displayPath(nodePath))
	}
	return loaded, node, nil
}

// describeScript assembles the document for one script node.
//
// The phase and what it runs around come from the file's *name*, which is the
// whole of how docs/FORMAT.md §2.4 decides: `_pre.js` and `_post.js` are the
// folder's, `create-order.post.js` belongs to `create-order.http`, and
// anything else is a module. The window says so rather than making the reader
// remember the convention.
func describeScript(node *collection.Node, text string) ScriptDocument {
	name := filepath.Base(node.Path)
	doc := ScriptDocument{
		Path:   node.ID,
		Name:   name,
		Text:   text,
		Hook:   node.Hook,
		HookOf: node.HookOf,
	}
	if node.Parent != nil {
		doc.Folder = node.Parent.ID
	}
	switch {
	case name == collection.PreHookName:
		doc.Phase, doc.Scope = "pre", doc.Folder
	case name == collection.PostHookName:
		doc.Phase, doc.Scope = "post", doc.Folder
	case node.Hook && strings.HasSuffix(name, collection.PreHookSuffix):
		doc.Phase = "pre"
	case node.Hook && strings.HasSuffix(name, collection.PostHookSuffix):
		doc.Phase = "post"
	}
	return doc
}
