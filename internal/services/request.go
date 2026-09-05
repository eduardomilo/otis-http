package services

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// AuthHeaderName is the header an @auth directive becomes at send time
// (docs/FORMAT.md §3.3).
const AuthHeaderName = "Authorization"

// Document is one request file as the editor needs it: the parsed model it
// edits, the raw text it was parsed from, and everything the file inherits
// with the provenance of each piece.
//
// The raw text travels with the model deliberately. The editor edits fields,
// but the diff view compares text, and a file Otis cannot parse still has to
// be openable — the raw is the escape hatch in both cases.
type Document struct {
	// Path is the node's collection-relative ID (docs/FORMAT.md §2.1).
	Path string `json:"path"`
	// Name is the display name: @name, then the "###" title, then the file
	// name without .http.
	Name string `json:"name"`
	// Raw is the file's bytes as text, exactly as read.
	Raw string `json:"raw"`
	// File is the parsed file, nil when ParseError is set.
	File *httpfile.File `json:"file"`
	// Index is the entry in File.Requests the editor shows: the first with a
	// request line, or 0 when the file has none.
	Index int `json:"index"`

	// Effective is what the request will send: headers in wire order, each
	// naming the file it came from, plus the nearest @auth (§3).
	Effective *resolve.Effective `json:"effective"`
	// Inherited is every header a folder above offered, including the ones a
	// nearer level overrode or switched off. Effective lists only what is
	// sent; this is what the Headers tab's INHERITED group draws, and it is
	// how an "!inherit" can be switched back on.
	Inherited []resolve.Inherited `json:"inherited"`
	// InheritedAuth is the @auth the folders above declare, ignoring this
	// file's own. It is what the Auth tab shows under "Inherit from folder"
	// and what "Override for this request" is prefilled from (screen 4b).
	InheritedAuth *resolve.Auth `json:"inheritedAuth"`
	// AuthHeader is the header @auth adds at send time, or nil when no auth
	// applies. It is a row in the Headers tab, tagged AUTH (screen 4a), and
	// it counts towards Counts.Sent.
	AuthHeader *AuthHeader `json:"authHeader"`
	// Counts is the "N sent · N local · N inherited" of screen 4a.
	Counts Counts `json:"counts"`
	// Chain names every file that contributes, root-most first, this file
	// last. Its length minus one is the "1 level" of screen 4a's status bar.
	Chain []string `json:"chain"`

	// Variables is every {{name}} the request references, resolved against
	// Env, in first-use order. A secret's value is never included.
	Variables []VariableRef `json:"variables"`
	// Env is the environment the variables were resolved against, "" for none.
	Env string `json:"env"`
	// EnvError is set when Env could not be read, so the editor can say the
	// variables are unresolved for a reason rather than looking broken.
	EnvError string `json:"envError,omitempty"`

	// ParseError is the parse error of an unparseable file, "" otherwise.
	// File is nil when it is set; Raw is still the file's text.
	ParseError string `json:"parseError,omitempty"`
	// InheritError is a malformed @auth above or in this file (§3.3 makes
	// that an error, not a warning). The document still opens.
	InheritError string `json:"inheritError,omitempty"`
	// Warnings are this file's collection warnings, already rendered.
	Warnings []string `json:"warnings,omitempty"`
}

// AuthHeader describes the Authorization header @auth becomes at send time.
// Value carries the directive's own text, so it may hold {{variables}} — it
// is never a resolved secret.
type AuthHeader struct {
	Name  string           `json:"name"`
	Value string           `json:"value"`
	Kind  resolve.AuthKind `json:"kind"`
	// Local is true when the @auth is in this file rather than above it.
	Local  bool           `json:"local"`
	Source resolve.Source `json:"source"`
}

// Counts is the header tally screen 4a shows on the sub-tab strip. Sent is
// what goes on the wire, so it includes the auth header and excludes anything
// an "!inherit" switched off.
type Counts struct {
	Sent      int `json:"sent"`
	Local     int `json:"local"`
	Inherited int `json:"inherited"`
}

// VariableRef is one {{name}} the request references.
type VariableRef struct {
	Name string `json:"name"`
	// Origin is where the value came from: "request", "folder", "env" or
	// "builtin". Empty when the name did not resolve.
	Origin resolve.Origin `json:"origin,omitempty"`
	Source resolve.Source `json:"source"`
	// Value is the resolved value. It is empty for a secret and for an
	// unresolved name, and a secret's value is never put here.
	Value string `json:"value,omitempty"`
	// Secret marks a value that lives in the OS keychain, or a file variable
	// that resolves to one. Its value is not in this struct.
	Secret bool `json:"secret,omitempty"`
	// Resolved is false for a name nothing defines. The editor styles those
	// as warnings (increment 10).
	Resolved bool `json:"resolved"`
}

// RequestService reads and writes one request file.
//
// It resolves against the collection CollectionService already walked, so the
// inheritance the editor shows is computed from the same files the sidebar
// drew. Every write goes through the write guard, or the watcher would report
// Otis' own save as an external change.
type RequestService struct {
	collections *CollectionService
}

// NewRequestService wires the service to the open collection.
func NewRequestService(collections *CollectionService) *RequestService {
	return &RequestService{collections: collections}
}

// Load reads one request file. nodePath is collection-relative; envName is the
// active environment, or "" for none.
//
// A file that does not parse is still returned, with ParseError set and Raw
// carrying its text: seeing the error and the text is the point.
func (s *RequestService) Load(nodePath, envName string) (Document, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return Document{}, err
	}
	node := loaded.Find(nodePath)
	if node == nil {
		return Document{}, fmt.Errorf("%s is not in the collection", nodePath)
	}
	if node.Kind != collection.KindRequest {
		return Document{}, fmt.Errorf("%s is a folder, not a request", nodePath)
	}
	raw, err := os.ReadFile(node.Path)
	if err != nil {
		return Document{}, fmt.Errorf("reading %s: %w", nodePath, err)
	}
	return s.describe(loaded, node, string(raw), envName), nil
}

// Save serializes file into the request at nodePath and writes it, then
// returns the document as it now stands on disk.
//
// The Go serializer is the only writer: it produces the canonical layout of
// docs/FORMAT.md §1.13, so a file the editor did not change is written back
// byte for byte. Sending text from the window instead would put a second
// formatter in the product.
func (s *RequestService) Save(nodePath, envName string, file httpfile.File) (Document, error) {
	return s.write(nodePath, envName, withoutBlankRows(file).String())
}

// withoutBlankRows drops every header and variable whose name is blank.
//
// The Headers tab's Add header and the folder editor's Add variable both add
// an empty row for the user to type into, which is the only way a table can
// offer a new row at all. Serialized, such a row is a line reading `: ` — not
// a header, and not something the parser will read back as one. It is a row
// somebody was in the middle of typing, so it is in the window and not in the
// file, exactly as an empty query parameter is in the Params table and not in
// the URL.
//
// It is applied where the file is written rather than where the row is added:
// the window is allowed to hold a half-finished edit, and Go is the thing
// that decides what a `.http` file may contain.
func withoutBlankRows(file httpfile.File) *httpfile.File {
	out := file
	out.Requests = make([]*httpfile.Request, 0, len(file.Requests))
	for _, entry := range file.Requests {
		if entry == nil {
			continue
		}
		copied := *entry
		copied.Headers = nil
		for _, h := range entry.Headers {
			if strings.TrimSpace(h.Name) != "" {
				copied.Headers = append(copied.Headers, h)
			}
		}
		copied.Variables = nil
		for _, v := range entry.Variables {
			if strings.TrimSpace(v.Name) != "" {
				copied.Variables = append(copied.Variables, v)
			}
		}
		out.Requests = append(out.Requests, &copied)
	}
	return &out
}

// SaveText writes text to the request at nodePath verbatim, after checking
// that it parses. It is the escape hatch for a file the structured editor
// cannot express — a raw edit, or a repair of a file that failed to parse.
func (s *RequestService) SaveText(nodePath, envName, text string) (Document, error) {
	if _, err := httpfile.ParseString(text); err != nil {
		return Document{}, fmt.Errorf("%s: %w", nodePath, err)
	}
	return s.write(nodePath, envName, text)
}

// Create writes a new request file in folderPath and returns its node path.
//
// The name is what the user typed; the file is named for its slug
// (collection.Slug, the same rules the Postman importer uses, docs/FORMAT.md
// §7) and the typed name is kept verbatim in an `# @name` directive, which is
// what the tree and the tabs display (§2.1). So "Create order" becomes
// create-order.http and still reads as "Create order" everywhere.
//
// A collision gets -2, -3, and so on rather than an error: the name a person
// types is a label, not an identifier, and two requests may reasonably want
// the same one.
//
// It does **not** touch `.order`. The new file is unlisted, so it sorts
// alphabetically after the listed ones, and that is the whole mechanism
// (docs/FORMAT.md §2.2). order.go stays the only writer of that file.
func (s *RequestService) Create(folderPath, name string) (string, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return "", err
	}
	folder := loaded.Find(folderPath)
	if folder == nil || folder.Kind != collection.KindFolder {
		return "", fmt.Errorf("%s is not a folder in this collection", displayPath(folderPath))
	}

	used := map[string]bool{}
	entries, err := os.ReadDir(folder.Path)
	if err != nil {
		return "", fmt.Errorf("creating a request in %s: %w", displayPath(folderPath), err)
	}
	for _, entry := range entries {
		used[strings.TrimSuffix(entry.Name(), collection.RequestExt)] = true
	}
	base := collection.UniqueName(used, collection.Slug(name), "request")

	nodePath := path.Join(folderPath, base+collection.RequestExt)
	if _, err := s.write(nodePath, "", newRequestText(name)); err != nil {
		return "", err
	}
	return nodePath, nil
}

// newRequestText is what a new request file starts as.
//
// The smallest thing that is a valid request and is obviously not finished: a
// name, and a GET at {{baseUrl}}. The reference rather than a literal URL
// because an environment is where a host belongs in this product (§4.3), and
// because an unresolved {{baseUrl}} shows up as exactly that in the editor —
// which is a truer first impression than a placeholder host that would appear
// to work.
func newRequestText(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "New request"
	}
	return fmt.Sprintf("# @name %s\nGET {{baseUrl}}/\n", trimmed)
}

// write is the one path that touches a request file on disk.
func (s *RequestService) write(nodePath, envName, text string) (Document, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return Document{}, err
	}
	// The node may not exist yet in the cached walk, but its file must be
	// inside the collection: absolutePath cleans the path first, so nothing
	// the window sends can address a file outside it.
	target := absolutePath(loaded.Dir, nodePath)
	if !strings.HasSuffix(target, collection.RequestExt) {
		return Document{}, fmt.Errorf("%s is not a %s file", nodePath, collection.RequestExt)
	}
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		return Document{}, fmt.Errorf("saving %s: %w", nodePath, err)
	}

	// Inside the guard: without it the watcher reports this write as someone
	// else's change and the window re-walks on every keystroke it just saved.
	release := s.collections.Guard().Writing(target)
	defer release()
	if err := writeFileAtomic(target, []byte(text)); err != nil {
		return Document{}, fmt.Errorf("saving %s: %w", nodePath, err)
	}
	// Re-walk and announce it, so the document that comes back, the next
	// Load, and the sidebar all see the file as it now is. The watcher will
	// not do this: the guard above exists precisely so that Otis' own write is
	// not reported as somebody else's.
	if err := s.collections.Refresh(); err != nil {
		return Document{}, err
	}
	return s.Load(nodePath, envName)
}

// describe assembles the document for one request node.
func (s *RequestService) describe(loaded *collection.Collection, node *collection.Node, raw, envName string) Document {
	doc := Document{
		Path:     node.ID,
		Name:     node.Name,
		Raw:      raw,
		File:     node.File,
		Warnings: warningsFor(loaded, node.ID),
	}
	if node.Broken {
		doc.ParseError = node.Error
		return doc
	}
	for i, entry := range node.File.Requests {
		if entry.HasRequestLine() {
			doc.Index = i
			break
		}
	}

	levels := resolve.LevelsOf(node)
	for _, lvl := range levels {
		doc.Chain = append(doc.Chain, lvl.Path)
	}
	eff, err := resolve.Levels(levels)
	if err != nil {
		// A malformed @auth is an error (§3.3), but the file still opens: the
		// Auth tab is where it gets fixed.
		doc.InheritError = err.Error()
		eff = &resolve.Effective{Headers: []resolve.Header{}}
	}
	doc.Effective = eff
	doc.Inherited = resolve.InheritedHeaders(levels)
	if doc.Inherited == nil {
		doc.Inherited = []resolve.Inherited{}
	}
	doc.InheritedAuth = resolve.AncestorAuth(levels)
	doc.AuthHeader = authHeader(eff, node.ID)
	doc.Counts = countHeaders(eff, node.ID, doc.AuthHeader)

	doc.Env = envName
	doc.Variables, doc.EnvError = s.describeVariables(loaded, node, levels, eff, envName)
	return doc
}

// describeVariables resolves the request's {{references}} for display.
//
// The store is secrets.Placeholder, never a real one: this is the path that
// answers a binding call, so nothing here may hold a secret value. A secret
// reference resolves to the placeholder, which is enough to tell "defined"
// from "missing" and to mark the variable as secret, and the value the editor
// receives is a value no keychain ever held.
func (s *RequestService) describeVariables(
	loaded *collection.Collection,
	node *collection.Node,
	levels []resolve.Level,
	eff *resolve.Effective,
	envName string,
) ([]VariableRef, string) {
	var env *resolve.Environment
	var envError string
	if envName != "" {
		var err error
		if env, err = resolve.LoadEnvironment(loaded.Dir, envName); err != nil {
			envError = err.Error()
		}
	}

	scope := resolve.NewScope(levels, env, secrets.Placeholder{}, resolve.CollectionKey(loaded))
	x := resolve.NewExpander(scope)
	// Every place §4.1 resolves references, in the order the editor reads
	// them: the URL, then the headers, then the auth arguments, then the body.
	entry := node.Request
	texts := []string{entry.URL}
	for _, h := range eff.Headers {
		texts = append(texts, h.Value)
	}
	if a := eff.Auth; a != nil {
		texts = append(texts, a.Token, a.Username, a.Password, a.Profile, a.AccessKey, a.SecretKey, a.SessionToken, a.Region, a.Service)
	}
	texts = append(texts, entry.Body.Raw)
	for _, text := range texts {
		if text == "" {
			continue
		}
		if _, err := x.Expand(text); err != nil {
			// A cycle or a store failure stops resolution; the names found so
			// far are still worth showing, so the error is not fatal here.
			break
		}
	}

	refs := make([]VariableRef, 0, len(x.Uses()))
	for _, u := range x.Uses() {
		ref := VariableRef{
			Name:     u.Name,
			Origin:   u.Origin,
			Source:   u.Source,
			Secret:   u.Secret,
			Resolved: true,
		}
		if !u.Secret {
			ref.Value = u.Value
		}
		refs = append(refs, ref)
	}
	if missing, ok := x.Err().(*resolve.MissingError); ok {
		for _, name := range missing.Names {
			refs = append(refs, VariableRef{Name: name})
		}
	}
	return refs, envError
}

// authHeader renders the effective @auth as the header it becomes at send
// time, or nil when nothing will be added.
//
// It returns nil when the effective headers already carry an Authorization:
// that header wins and the @auth is dropped (§3.3), so claiming the auth row
// would be claiming a header the request does not send.
func authHeader(eff *resolve.Effective, requestPath string) *AuthHeader {
	if eff.Auth == nil || eff.Auth.Kind == resolve.AuthNone {
		return nil
	}
	if _, ok := eff.Header(AuthHeaderName); ok {
		return nil
	}
	value := ""
	switch eff.Auth.Kind {
	case resolve.AuthBearer:
		value = "Bearer " + eff.Auth.Token
	case resolve.AuthBasic:
		// The credentials are base64 of "user:password" at send time. Showing
		// the encoding would be showing the password, so the row names the
		// scheme and the user only.
		value = "Basic " + eff.Auth.Username
	case resolve.AuthAWS:
		// AWS SigV4 is a signature over the whole request, not a fixed value,
		// and the design has no representation for it (DESIGN-NOTES §9.2).
		value = "AWS4-HMAC-SHA256 (signed at send time)"
	}
	return &AuthHeader{
		Name:   AuthHeaderName,
		Value:  value,
		Kind:   eff.Auth.Kind,
		Local:  eff.Auth.Source.Path == requestPath,
		Source: eff.Auth.Source,
	}
}

// countHeaders is the "N sent · N local · N inherited" tally of screen 4a.
// Sent is local plus inherited, so the three always add up on screen.
func countHeaders(eff *resolve.Effective, requestPath string, auth *AuthHeader) Counts {
	var c Counts
	for _, h := range eff.Headers {
		if h.Source.Path == requestPath {
			c.Local++
		} else {
			c.Inherited++
		}
	}
	if auth != nil {
		if auth.Local {
			c.Local++
		} else {
			c.Inherited++
		}
	}
	c.Sent = c.Local + c.Inherited
	return c
}

// warningsFor returns the collection warnings that belong to one node, in the
// same rendered form the tree uses.
func warningsFor(loaded *collection.Collection, id string) []string {
	var out []string
	for _, w := range loaded.Warnings {
		if w.Path == id {
			out = append(out, string(w.Code)+": "+w.Msg)
		}
	}
	return out
}

// writeFileAtomic writes data to a temp file in the same directory and renames
// it over the target, so a crash mid-write cannot truncate a request file.
func writeFileAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename below has succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// A new file must not inherit the temp file's 0600: a request file is
	// meant to be committed and read by everyone who checks the repo out.
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, target)
}

// Rename gives the request at nodePath a new name and returns its new node
// path, which the caller navigates to.
//
// It changes both halves of a request's identity, and does so together: the
// `# @name` directive becomes what was typed, and the file is renamed to that
// name's slug. That is deliberate symmetry with Create, which writes both from
// one typed name for the same reason — the file name and the tree label are
// two views of the same thing, and a rename that moved only one of them would
// leave a `place-order.http` called "Create order" that nobody meant. The
// dialog shows both lines before it happens (DESIGN-NOTES §8.2).
//
// A file that did not parse is refused rather than renamed, because half of
// this operation is rewriting its contents and Otis does not rewrite a file it
// could not read — the same stance the folder settings editor takes.
//
// It keeps `.order` in step, and only in step: if the folder has one and the
// entry is listed, that one line is rewritten so the request stays where it
// was. A folder with no `.order` does not acquire one.
func (s *RequestService) Rename(nodePath, name string) (string, error) {
	_, node, err := s.requestNode(nodePath)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("a request needs a name")
	}
	if node.Broken {
		return "", fmt.Errorf("%s did not parse, so Otis will not rewrite it: %s", displayPath(nodePath), node.Error)
	}
	parent := node.Parent
	if parent == nil {
		return "", fmt.Errorf("%s has no folder", displayPath(nodePath))
	}

	raw, err := os.ReadFile(node.Path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", displayPath(nodePath), err)
	}
	file, err := httpfile.ParseString(string(raw))
	if err != nil {
		return "", fmt.Errorf("%s: %w", displayPath(nodePath), err)
	}
	setRequestName(file, trimmed)

	used, err := namesInUse(parent.Path)
	if err != nil {
		return "", err
	}
	current := strings.TrimSuffix(filepath.Base(node.Path), collection.RequestExt)
	delete(used, current)
	base := collection.UniqueName(used, collection.Slug(trimmed), "request")
	target := filepath.Join(parent.Path, base+collection.RequestExt)

	release := s.collections.Guard().Writing(
		node.Path, target, parent.Path, filepath.Join(parent.Path, collection.OrderFileName))
	err = writeFileAtomic(node.Path, []byte(file.String()))
	if err == nil && target != node.Path {
		if err = os.Rename(node.Path, target); err == nil {
			err = renameInOrder(parent, entryKey(node), base+collection.RequestExt)
		}
	}
	release()
	if err != nil {
		return "", fmt.Errorf("renaming %s: %w", displayPath(nodePath), err)
	}
	if err := s.collections.Refresh(); err != nil {
		return "", err
	}
	return path.Join(parent.ID, base+collection.RequestExt), nil
}

// Duplicate copies the request at nodePath into the same folder and returns
// the copy's node path.
//
// The copy is "<name> copy", and "<name> copy 2" if that is taken, with the
// file named for the slug of whichever it settled on — so the label in the
// tree and the name on disk never disagree, however many times it is used.
//
// Like Create it does not touch `.order`: the copy is unlisted and sorts
// alphabetically after the listed entries, which is the whole mechanism
// (docs/FORMAT.md §2.2).
func (s *RequestService) Duplicate(nodePath string) (string, error) {
	_, node, err := s.requestNode(nodePath)
	if err != nil {
		return "", err
	}
	parent := node.Parent
	if parent == nil {
		return "", fmt.Errorf("%s has no folder", displayPath(nodePath))
	}
	raw, err := os.ReadFile(node.Path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", displayPath(nodePath), err)
	}

	used, err := namesInUse(parent.Path)
	if err != nil {
		return "", err
	}
	label, base := copyName(used, node.Name)

	// A file that did not parse is copied verbatim: it cannot carry a new
	// @name, and refusing to duplicate it would be refusing the one operation
	// that lets somebody keep the broken original while they repair a copy.
	text := string(raw)
	if !node.Broken {
		if file, err := httpfile.ParseString(text); err == nil {
			setRequestName(file, label)
			text = file.String()
		}
	}

	target := path.Join(parent.ID, base+collection.RequestExt)
	if _, err := s.write(target, "", text); err != nil {
		return "", err
	}
	return target, nil
}

// Delete removes the request at nodePath.
//
// There is no confirmation parameter: unlike discarding a hunk, which is one
// of several things the diff view's buttons do and which had to be
// unreachable by accident, this method's name is the whole of what it does.
// The dialog in front of it is the safety, and it says whether git still has
// a copy.
//
// It drops the entry from the folder's `.order` when one lists it, for the
// same reason a rename rewrites the line: a `.order` naming a file that is not
// there produces a warning on every walk (docs/FORMAT.md §2.2), and the user
// did not leave it behind — Otis did.
func (s *RequestService) Delete(nodePath string) error {
	_, node, err := s.requestNode(nodePath)
	if err != nil {
		return err
	}
	return removeNode(s.collections, node)
}

// removeNode deletes a node's file or directory and tidies the parent's
// `.order`. Shared by RequestService.Delete and FolderService.Delete, which
// is why it is a function over the collection service rather than a method on
// either: deleting a folder and deleting a request differ only in what
// `RemoveAll` finds there.
func removeNode(collections *CollectionService, node *collection.Node) error {
	parent := node.Parent
	paths := []string{node.Path}
	if parent != nil {
		paths = append(paths, parent.Path, filepath.Join(parent.Path, collection.OrderFileName))
	}
	release := collections.Guard().Writing(paths...)
	err := os.RemoveAll(node.Path)
	if err == nil && parent != nil {
		err = dropFromOrder(parent, entryKey(node))
	}
	release()
	if err != nil {
		return fmt.Errorf("deleting %s: %w", displayPath(node.ID), err)
	}
	return collections.Refresh()
}

// requestNode resolves a node path to a request in the cached walk.
func (s *RequestService) requestNode(nodePath string) (*collection.Collection, *collection.Node, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return nil, nil, err
	}
	node := loaded.Find(nodePath)
	if node == nil {
		return nil, nil, fmt.Errorf("%s is not in the collection", displayPath(nodePath))
	}
	if node.Kind != collection.KindRequest {
		return nil, nil, fmt.Errorf("%s is not a request", displayPath(nodePath))
	}
	return loaded, node, nil
}

// setRequestName writes `# @name` on the entry the editor shows — the first
// with a request line, or the first entry in a file that has none.
//
// A replaced directive keeps its line number, which holds its position in the
// preamble, so renaming does not move the line. A new one has none and is
// written after whatever the file already had, which is the same rule every
// other directive Otis adds follows (see the serializer's preamble ordering).
func setRequestName(file *httpfile.File, name string) {
	entry := (*httpfile.Request)(nil)
	for _, r := range file.Requests {
		if r.HasRequestLine() {
			entry = r
			break
		}
	}
	if entry == nil {
		if len(file.Requests) == 0 {
			return
		}
		entry = file.Requests[0]
	}
	kept := make([]httpfile.Directive, 0, len(entry.Directives)+1)
	line, style := 0, "#"
	for _, d := range entry.Directives {
		if d.Name == "name" {
			if line == 0 {
				line, style = d.Line, d.Style
			}
			continue
		}
		kept = append(kept, d)
	}
	entry.Directives = append(kept, httpfile.Directive{Style: style, Name: "name", Value: name, Line: line})
	// The "###" title is the other place a name can live (§1.4). Leaving it
	// would mean the file still says the old name in a second spelling, and
	// @name wins — so a title that was the name is cleared rather than left
	// to contradict the directive.
	if entry.Title != "" {
		entry.Title = ""
	}
}

// namesInUse is every base name already taken in a directory: file names
// without their .http suffix, and directory names. Both, because a request
// and a folder cannot share one.
func namesInUse(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	used := make(map[string]bool, len(entries))
	for _, entry := range entries {
		used[strings.TrimSuffix(entry.Name(), collection.RequestExt)] = true
	}
	return used, nil
}

// copyName names a duplicate: "<name> copy", then "<name> copy 2", "copy 3"
// and so on until the slug is free, returning both the label and the slug.
//
// Both together, because they have to agree. Naming the file with
// UniqueName's -2 suffix while the label stayed "<name> copy" would put two
// rows reading the same thing in the tree, which is exactly what a duplicate
// most needs not to do.
func copyName(used map[string]bool, name string) (label, base string) {
	if strings.TrimSpace(name) == "" {
		name = "request"
	}
	for i := 1; ; i++ {
		label = name + " copy"
		if i > 1 {
			label = fmt.Sprintf("%s copy %d", name, i)
		}
		base = collection.Slug(label)
		if base == "" {
			base = collection.UniqueName(used, "", "request-copy")
			return label, base
		}
		if !used[base] {
			used[base] = true
			return label, base
		}
	}
}
