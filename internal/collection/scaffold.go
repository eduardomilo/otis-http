package collection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/otis-http/otis/internal/httpfile"
)

// Scaffolding is what a brand-new collection contains.
//
// Screen 2b's "Start fresh" card promises "an empty collection folder with one
// example request and a local environment", and this is that promise as data:
// three files, no more. It is here rather than in the service because it is
// the answer to "what is a collection, minimally" — the same question
// FindRoot answers from the other direction — and because a pure value is a
// thing a test can read.
//
// The `.http` files are built as parsed models and serialized by
// `internal/httpfile`, never written as strings. Go's serializer is the only
// writer of a `.http` file (CLAUDE.md), and a hand-typed scaffold that was one
// space off canonical form would put a diff in front of whoever saved the file
// first — on their first minute with the app.
type Scaffolding struct {
	// Files are the paths, relative to the collection root, and their
	// contents, in the order they should be written.
	Files []ScaffoldFile
}

// ScaffoldFile is one file of a scaffold.
type ScaffoldFile struct {
	// Path is relative to the collection root, with "/" separators.
	Path string
	// Body is the file's exact contents.
	Body []byte
}

// DefaultCollectionName is what a new collection is called when nothing else
// names it. A dotted directory beside the code it exercises is the convention
// docs/FORMAT.md §5 and the design both use (`~/code/acme-api/.requests`).
const DefaultCollectionName = ".requests"

// DefaultEnvironmentName is the environment a new collection starts with. It
// is `local` and not `dev`, because the one environment you are certain to
// want on the first day is the one pointing at the thing running on your own
// machine.
const DefaultEnvironmentName = "local"

// Scaffold returns the files a new collection starts with.
//
// It takes no arguments on purpose. A scaffold that varied with the folder's
// name would be a template language, and the first thing anybody does to
// these three files is edit them.
func Scaffold() Scaffolding {
	return Scaffolding{Files: []ScaffoldFile{
		{Path: FolderFileName, Body: []byte(rootFolderFile())},
		{Path: EnvDirName + "/" + DefaultEnvironmentName + ".json", Body: localEnvironment()},
		{Path: "example.http", Body: []byte(exampleRequest())},
	}}
}

// rootFolderFile is the collection's own `_folder.http`.
//
// It is comments only, and — this is the part with a test on it — comments
// that cannot be read as anything else. A commented-out example of the form
// "# @auth bearer {{apiToken}}" is not an example: `@` after the marker is
// exactly what makes a directive (docs/FORMAT.md §3), so the first draft of
// this file shipped live auth on every new collection, sending
// `Authorization: Bearer {{apiToken}}` at a variable nothing defines. The
// guidance is prose, and it points at the editor rather than at a syntax.
//
// A collection whose first act was to set an auth scheme nobody asked for
// would be a surprise in any case. What this file is for is to be *there*:
// it is where a whole
// collection's shared auth, headers and variables belong in
// (docs/FORMAT.md §2.1), it is where the folder view's Edit writes, and git
// does not track an empty directory, so a folder without one can vanish on
// somebody else's clone (CLAUDE.md).
func rootFolderFile() string {
	f := &httpfile.File{Requests: []*httpfile.Request{{
		Comments: []httpfile.Comment{
			{Style: "#", Text: " Settings shared by every request in this collection."},
			{Style: "#", Text: " Auth, headers and variables written here cascade down; a request or a"},
			{Style: "#", Text: " nearer folder can override any of them."},
			{Style: "#", Text: ""},
			{Style: "#", Text: " Open the collection root in Otis to edit them: the Auth, Variables and"},
			{Style: "#", Text: " Headers tabs write this file."},
		},
	}}}
	return f.String()
}

// exampleRequest is the one request a new collection starts with.
//
// It references {{baseUrl}} because that is the thing worth demonstrating:
// the request does not name an environment, the environment names the host,
// and switching environments switches where this goes (docs/FORMAT.md §4).
func exampleRequest() string {
	f := &httpfile.File{Requests: []*httpfile.Request{{
		Directives: []httpfile.Directive{
			{Style: "#", Name: "name", Value: "Example"},
		},
		Comments: []httpfile.Comment{
			{Style: "#", Text: " baseUrl comes from env/" + DefaultEnvironmentName + ".json."},
			{Style: "#", Text: " Switch environments in the title bar to send this somewhere else."},
		},
		Method:  "GET",
		URL:     "{{baseUrl}}/health",
		Headers: []httpfile.Header{{Name: "Accept", Value: "application/json"}},
	}}}
	return f.String()
}

// localEnvironment is `env/local.json`, with one variable in it.
//
// Written through encoding/json rather than as a string for the same reason
// the `.http` files go through the serializer: the environment editor rewrites
// this file, and the scaffold should already be in the shape it rewrites it
// to.
func localEnvironment() []byte {
	data, err := json.MarshalIndent(map[string]string{
		"baseUrl": "http://localhost:8080",
	}, "", "  ")
	if err != nil {
		// map[string]string cannot fail to marshal; the branch exists so the
		// error is not silently dropped if that ever stops being true.
		return []byte("{\n  \"baseUrl\": \"http://localhost:8080\"\n}\n")
	}
	return append(data, '\n')
}

// WriteScaffold creates dir and writes the scaffold into it.
//
// The directory must not already contain anything: a scaffold is what you get
// when there is nothing, and writing three files over somebody's folder
// because they picked the wrong one in a dialog is not recoverable from
// inside Otis. An existing *empty* directory is fine — that is what a person
// who just made one in the picker has.
func WriteScaffold(dir string) error {
	if err := ensureEmptyDir(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	for _, file := range Scaffold().Files {
		path := filepath.Join(dir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, file.Body, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// ensureEmptyDir reports an error unless dir is absent or empty.
func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", dir)
	}
	return nil
}

// FindWithin locates the collection inside a directory that was just cloned.
//
// FindRoot walks *up* from a file, which is the right question for "which
// collection does this request belong to". This is the other one: a clone has
// just landed and the collection is somewhere in it. A repository whose whole
// content is the collection is the common case and answers immediately; a
// repository with the collection in a subdirectory — `.requests` beside the
// code, which is the convention — is the other one worth handling.
//
// It looks one level down and no further. A deeper search would start finding
// fixtures and vendored test data, and being wrong here means opening the
// wrong thing rather than opening nothing.
//
// Returns "" when nothing in the clone looks like a collection, which is a
// real answer: the caller says where the clone went and leaves it open.
func FindWithin(dir string) string {
	if looksLikeCollection(dir) {
		return dir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	// `.requests` first, whatever the directory order is, so the convention
	// wins over a sibling that happens to sort earlier.
	preferred := filepath.Join(dir, DefaultCollectionName)
	if looksLikeCollection(preferred) {
		return preferred
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".git" {
			continue
		}
		candidate := filepath.Join(dir, entry.Name())
		if looksLikeCollection(candidate) {
			return candidate
		}
	}
	return ""
}

// looksLikeCollection reports whether dir is plausibly a collection root: it
// carries one of the markers FindRoot recognises, or a `.http` file.
func looksLikeCollection(dir string) bool {
	if hasMarker(dir) {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == RequestExt {
			return true
		}
	}
	return false
}
