package services

import (
	"fmt"
	"os"
	"path"
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

// ScriptKind is what a new script will be, which docs/FORMAT.md §2.4 decides
// entirely from its file name.
//
// The window picks the kind and Go picks the name, rather than the window
// typing `_post.js` and hoping: the convention is easy to get subtly wrong —
// `utils.pre.js` beside no `utils.http` is a module with an unfortunate name,
// not a hook — and the whole point of §2.4 having a table is that a person
// should not have to hold it in their head.
const (
	// ScriptFolderHook is `_pre.js` or `_post.js`: it runs around every
	// request in its folder and below.
	ScriptFolderHook = "folder"
	// ScriptRequestHook is `<slug>.pre.js` or `<slug>.post.js` beside
	// `<slug>.http`: it runs around that one request.
	ScriptRequestHook = "request"
	// ScriptModule is any other `.js`: nothing runs it unless a hook imports
	// it.
	ScriptModule = "module"
)

// NewScript is what the create dialog asks for.
type NewScript struct {
	// Kind is one of the three constants above.
	Kind string `json:"kind"`
	// Folder is where a folder hook or a module goes, "" for the root.
	// Ignored for a request hook, which goes beside its request.
	Folder string `json:"folder"`
	// Phase is "pre" or "post". Required for both hook kinds, ignored for a
	// module.
	Phase string `json:"phase"`
	// Request is the node path of the request a request hook belongs to.
	Request string `json:"request"`
	// Name is the module's file name. Ignored for a hook, whose name is not
	// a choice.
	Name string `json:"name"`
}

// Plan reports the file a NewScript would create and the sentence that says
// what runs it, without creating anything.
//
// It exists so the dialog can show both while you are still choosing — the
// same promise the create dialog makes for a request (DESIGN-NOTES §8.2) —
// and it is Go's answer rather than a mirror in the window, because the
// naming rule is §2.4's and there must be one implementation of it.
func (s *ScriptService) Plan(req NewScript) (ScriptPlan, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return ScriptPlan{}, err
	}
	nodePath, err := plannedScriptPath(loaded, req)
	if err != nil {
		return ScriptPlan{Problem: err.Error()}, nil
	}
	plan := ScriptPlan{Path: nodePath, Runs: scriptRuns(req, nodePath)}
	if loaded.Find(nodePath) != nil {
		plan.Problem = displayPath(nodePath) + " already exists"
		return plan, nil
	}
	// The walk is a cache, so the disk is the authority on whether the name
	// is free — a file can arrive between two walks, and creating over a
	// script somebody else just wrote is not recoverable from inside Otis.
	if _, statErr := os.Stat(absolutePath(loaded.Dir, nodePath)); statErr == nil {
		plan.Problem = displayPath(nodePath) + " already exists"
	}
	return plan, nil
}

// ScriptPlan is what Plan answers with.
type ScriptPlan struct {
	// Path is the node path the script would be created at.
	Path string `json:"path"`
	// Runs is the sentence saying what runs it, the same one the editor's
	// header shows once it exists.
	Runs string `json:"runs"`
	// Problem is why it cannot be created, or "". A plan with a problem
	// still carries its path, because naming the file in the way is most of
	// the explanation.
	Problem string `json:"problem"`
}

// Create writes a new script and returns its node path.
//
// The starter text is one comment line saying what runs it, and this is the
// only time Otis writes into a `.js` at all — Save is verbatim and always
// will be. A line naming what runs a file is worth having for the colleague
// who meets it in a terminal rather than in this window, and it is a fact
// about the file name rather than an opinion about JavaScript.
func (s *ScriptService) Create(req NewScript) (string, error) {
	plan, err := s.Plan(req)
	if err != nil {
		return "", err
	}
	if plan.Problem != "" {
		return "", fmt.Errorf("%s", plan.Problem)
	}
	loaded, err := s.collections.Loaded()
	if err != nil {
		return "", err
	}
	target := absolutePath(loaded.Dir, plan.Path)

	// Inside the guard, like every write Otis makes, and `.order` is not
	// touched: the new file is unlisted, so it sorts alphabetically after the
	// listed ones, and that is the whole mechanism (docs/FORMAT.md §2.2).
	release := s.collections.Guard().Writing(target)
	err = writeFileAtomic(target, []byte("// "+plan.Runs+"\n\n"))
	release()
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", displayPath(plan.Path), err)
	}
	if err := s.collections.Refresh(); err != nil {
		return "", err
	}
	return plan.Path, nil
}

// plannedScriptPath is §2.4's table, as a function.
func plannedScriptPath(loaded *collection.Collection, req NewScript) (string, error) {
	phase := strings.ToLower(strings.TrimSpace(req.Phase))
	if req.Kind != ScriptModule && phase != "pre" && phase != "post" {
		return "", fmt.Errorf("a hook runs either before the request or after the response")
	}
	switch req.Kind {
	case ScriptFolderHook:
		folder := loaded.Find(req.Folder)
		if folder == nil || folder.Kind != collection.KindFolder {
			return "", fmt.Errorf("%s is not a folder in this collection", displayPath(req.Folder))
		}
		name := collection.PreHookName
		if phase == "post" {
			name = collection.PostHookName
		}
		return path.Join(req.Folder, name), nil

	case ScriptRequestHook:
		request := loaded.Find(req.Request)
		if request == nil || request.Kind != collection.KindRequest {
			return "", fmt.Errorf("%s is not a request in this collection", displayPath(req.Request))
		}
		// Beside the request and named for it, which is the only way §2.4
		// makes it a hook rather than a module with an unfortunate name.
		base := strings.TrimSuffix(req.Request, collection.RequestExt)
		suffix := collection.PreHookSuffix
		if phase == "post" {
			suffix = collection.PostHookSuffix
		}
		return base + suffix, nil

	case ScriptModule:
		folder := loaded.Find(req.Folder)
		if folder == nil || folder.Kind != collection.KindFolder {
			return "", fmt.Errorf("%s is not a folder in this collection", displayPath(req.Folder))
		}
		name, err := moduleFileName(req.Name)
		if err != nil {
			return "", err
		}
		return path.Join(req.Folder, name), nil
	}
	return "", fmt.Errorf("%q is not a kind of script", req.Kind)
}

// moduleFileName turns a typed name into a `.js` file name.
//
// Not `collection.Slug`, which is how a *request* is named: a request's file
// name is derived from a display name that lives in the file, so the slug is
// a translation between two representations of one thing. A module has no
// display name — its file name is its whole identity and it is also the
// import specifier a hook will type — so what is typed is what is written,
// minus a `.js` the person may have added themselves. Otis has no opinion
// about JavaScript, and that includes what its files are called.
func moduleFileName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimSuffix(trimmed, collection.ScriptExt)
	if trimmed == "" {
		return "", fmt.Errorf("give the module a name")
	}
	if trimmed == "." || trimmed == ".." || strings.ContainsAny(trimmed, `/\`) {
		return "", fmt.Errorf("%q is not a file name", name)
	}
	// A leading underscore is how §2.4 spells a folder hook, so a module
	// called `_helpers` would be a file whose name says it runs on its own
	// and does not.
	if strings.HasPrefix(trimmed, "_") {
		return "", fmt.Errorf("a name starting with _ is reserved for folder hooks")
	}
	// `<name>.pre` and `<name>.post` are how §2.4 spells a request hook. One
	// beside a matching `.http` would silently become a hook; one beside no
	// matching request is the "module with an unfortunate name" §2.4 warns
	// about. Neither is what someone naming a module meant.
	if strings.HasSuffix(trimmed, ".pre") || strings.HasSuffix(trimmed, ".post") {
		return "", fmt.Errorf("names ending .pre or .post are how a request hook is spelled")
	}
	return trimmed + collection.ScriptExt, nil
}

// scriptRuns is the sentence that says what runs a script, in the same words
// the editor's header uses once the file exists.
func scriptRuns(req NewScript, nodePath string) string {
	where := "the collection root"
	if req.Folder != "" {
		where = req.Folder + "/"
	}
	switch req.Kind {
	case ScriptFolderHook:
		if strings.EqualFold(req.Phase, "post") {
			return "Runs after every response in " + where + " and below."
		}
		return "Runs before every request in " + where + " and below."
	case ScriptRequestHook:
		if strings.EqualFold(req.Phase, "post") {
			return "Runs after " + displayPath(req.Request) + ", and only that request."
		}
		return "Runs before " + displayPath(req.Request) + ", and only that request."
	}
	return "A module: nothing runs this unless a hook imports it."
}
