package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// ReadmeName is the documentation file a folder view renders (screen 3a).
const ReadmeName = "README.md"

// FolderCounts is what the folder header says about a folder's contents.
//
// Direct and recursive are both here, and both labelled, because the design
// says two different things and DESIGN-NOTES §9.6 flagged it: the header reads
// "5 requests · 1 subfolder" (direct) while the auth panel reads "inherited by
// 6 requests" (recursive, including the one in fixtures/). Both are right. The
// rule now is that a count describing the folder's *contents* is direct and a
// count describing its *reach* is recursive, and the labels say which.
type FolderCounts struct {
	// Requests and Subfolders are the folder's direct children.
	Requests   int `json:"requests"`
	Subfolders int `json:"subfolders"`
	// Below is every request in the subtree — what the settings here reach.
	Below int `json:"below"`
	// Scripts is the folder's own hooks and modules.
	Scripts int `json:"scripts"`
}

// FolderAuth is the auth every request below a folder starts with.
type FolderAuth struct {
	// Kind is "bearer", "basic", "aws", "none", or "" when no level declares
	// any auth at all — which is distinct from "none" (docs/FORMAT.md §3.3).
	Kind resolve.AuthKind `json:"kind"`
	// Summary describes the scheme in words, e.g. "Bearer token".
	Summary string `json:"summary"`
	// Token, Username, Region, Service and Profile carry the arguments that
	// are safe to show. A secret is never among them: the token is the
	// {{reference}} as written, not its value.
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Region   string `json:"region,omitempty"`
	Service  string `json:"service,omitempty"`
	// Sends is what goes on the wire, masked: "Authorization: Bearer •••••".
	Sends string `json:"sends,omitempty"`
	// Secret reports that resolving the arguments would consume a secret, so
	// the panel can show the keychain lock the design puts beside the token.
	Secret bool `json:"secret,omitempty"`
	// Source is the file and line the auth came from. Local is true when that
	// file is this folder's own _folder.http rather than an ancestor's.
	Source resolve.Source `json:"source"`
	Local  bool           `json:"local"`
	// Error is a malformed @auth, which is an error and not a warning
	// (§3.3). The panel says so rather than showing nothing.
	Error string `json:"error,omitempty"`
}

// FolderHeader is one header every request below a folder starts with.
type FolderHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Source is the _folder.http and line it came from; Local is true for
	// this folder's own.
	Source resolve.Source `json:"source"`
	Local  bool           `json:"local"`
}

// FolderVariable is one committed variable in scope at a folder.
type FolderVariable struct {
	Name   string         `json:"name"`
	Value  string         `json:"value"`
	Source resolve.Source `json:"source"`
	Local  bool           `json:"local"`
}

// FolderScript is one script file belonging to a folder.
type FolderScript struct {
	// Path is collection-relative.
	Path string `json:"path"`
	Name string `json:"name"`
	// Hook is "pre", "post", or "" for a module.
	Hook string `json:"hook,omitempty"`
	// Lines is the file's line count, which is what the design shows beside
	// each hook.
	Lines int `json:"lines"`
	// Source is the file's text. Hooks are shown verbatim in the panel; a
	// module is listed but not quoted, because nothing runs it here.
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

// FolderOverride is a descendant that does not take one of the folder's
// settings.
//
// The design's Overrides row ("fixtures/seed-order uses none") is the honest
// half of an inheritance model: a panel that says "inherited by 6 requests"
// without saying which of them opted out is telling you something untrue about
// at least one of them.
type FolderOverride struct {
	// Path is the descendant's node path.
	Path string `json:"path"`
	Name string `json:"name"`
	// What is "auth" or a header name.
	What string `json:"what"`
	// How describes the override in words: "uses none", "sets its own",
	// "switched off with !inherit".
	How string `json:"how"`
	// Source is where the descendant does it.
	Source resolve.Source `json:"source"`
}

// FolderDocument is a folder open in the folder view (screen 3a).
type FolderDocument struct {
	// Path is the folder's node path, "" for the collection root.
	Path string `json:"path"`
	// Name is the directory name, or the collection's display name at the
	// root.
	Name   string       `json:"name"`
	Counts FolderCounts `json:"counts"`

	// SettingsPath is "orders/_folder.http", empty when the folder has none.
	SettingsPath string `json:"settingsPath,omitempty"`
	// Settings is the parsed _folder.http, for editing. The frontend changes
	// this model and hands it back to Save; Go's serializer is still the only
	// thing that writes the file.
	Settings *httpfile.File `json:"settings,omitempty"`
	// SettingsError is a _folder.http that does not parse. The folder still
	// opens: seeing the error is the point (§3.4).
	SettingsError string `json:"settingsError,omitempty"`

	// ReadmePath and Readme are the folder's README.md, empty when absent.
	ReadmePath string `json:"readmePath,omitempty"`
	Readme     string `json:"readme,omitempty"`

	Auth      *FolderAuth            `json:"auth,omitempty"`
	Headers   []FolderHeader         `json:"headers"`
	Variables []FolderVariable       `json:"variables"`
	Session   []resolve.SessionValue `json:"session"`
	Scripts   []FolderScript         `json:"scripts"`
	Overrides []FolderOverride       `json:"overrides"`

	// References is every {{name}} the folder's own values use, resolved
	// against the active environment, with its origin and provenance. It is
	// the index the `{{token}}` styling consults, so a reference to an
	// environment secret reads as resolved rather than as a warning — the
	// same shape the request editor gets.
	//
	// No secret value is in it: the resolution runs against
	// secrets.Placeholder, which is enough to tell "defined" from "missing".
	References []VariableRef `json:"references"`

	// Inheriting is every request below the folder, in display order. It is
	// what "inherited by N requests" counts and what Run folder runs.
	Inheriting []string `json:"inheriting"`
	// Env is the environment the values were described against, "" for none.
	Env string `json:"env,omitempty"`
}

// FolderService is the folder view's service (screen 3a): it reads a folder's
// shared settings with their provenance, the documentation beside them, the
// scripts that run around every request below, and which descendants opt out
// of each setting. It is the only thing that writes a `_folder.http`.
type FolderService struct {
	app         *application.App
	collections *CollectionService
	// sessions supplies the variables a run set, which are the other half of
	// the Variables panel and live nowhere on disk (docs/FORMAT.md §4.5).
	sessions *SendService
}

// NewFolderService constructs the service. sends may be nil, in which case the
// session half of the Variables panel is simply empty.
func NewFolderService(collections *CollectionService, sends *SendService) *FolderService {
	return &FolderService{collections: collections, sessions: sends}
}

// ServiceStartup resolves the application.
func (s *FolderService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// Load reads one folder. nodePath is collection-relative; "" is the root.
// envName is the active environment, used only to describe which values a
// {{reference}} would resolve to — never to fetch a secret.
func (s *FolderService) Load(nodePath, envName string) (FolderDocument, error) {
	loaded, folder, err := s.folder(nodePath)
	if err != nil {
		return FolderDocument{}, err
	}

	doc := FolderDocument{Path: folder.ID, Name: folder.Name, Env: envName}
	if folder.Parent == nil {
		doc.Name = collection.DisplayName(loaded.Dir)
	}
	if folder.SettingsPath != "" {
		doc.SettingsPath = path.Join(folder.ID, collection.FolderFileName)
		doc.Settings = folder.Settings
		if folder.Settings == nil {
			doc.SettingsError = "this file could not be parsed, so the folder contributes no settings"
		}
	}
	s.readReadme(&doc, folder)
	s.describeCounts(&doc, folder)
	s.describeScripts(&doc, folder)
	s.describeSettings(&doc, loaded, folder, envName)
	s.describeOverrides(&doc, folder)
	if s.sessions != nil {
		doc.Session = s.sessions.SessionScope(resolve.SessionFolder, folder.ID)
	}
	if doc.Session == nil {
		doc.Session = []resolve.SessionValue{}
	}
	return doc, nil
}

// Save writes a folder's _folder.http and returns the folder as it now is.
//
// The frontend edits the parsed model and hands it back, exactly as the
// request editor does: Go's serializer stays the only writer of a .http file,
// so there is one answer to what canonical form is (docs/FORMAT.md §1.13).
// A folder with no settings file yet gets one.
func (s *FolderService) Save(nodePath, envName string, file httpfile.File) (FolderDocument, error) {
	loaded, folder, err := s.folder(nodePath)
	if err != nil {
		return FolderDocument{}, err
	}
	// A request line in a folder file is ignored with a warning when read
	// (§2.3); refusing to write one is better than writing something that
	// will be ignored.
	for _, entry := range file.Requests {
		if entry.HasRequestLine() {
			return FolderDocument{}, fmt.Errorf(
				"%s holds settings, not requests: remove the %s line", collection.FolderFileName, entry.Method)
		}
	}

	target := filepath.Join(folder.Path, collection.FolderFileName)
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		return FolderDocument{}, fmt.Errorf("saving %s: %w", collection.FolderFileName, err)
	}

	// Inside the guard, like every write Otis makes.
	release := s.collections.Guard().Writing(target)
	err = writeFileAtomic(target, []byte(file.String()))
	release()
	if err != nil {
		return FolderDocument{}, fmt.Errorf("saving %s: %w", path.Join(folder.ID, collection.FolderFileName), err)
	}
	// The guard means the watcher will not announce this, so the write does.
	// Every request below now inherits something different, which is why the
	// whole tree is re-walked rather than this folder alone.
	if err := s.collections.Refresh(); err != nil {
		return FolderDocument{}, err
	}
	_ = loaded
	return s.Load(nodePath, envName)
}

// Create makes a new folder inside parentPath and returns its node path.
//
// It writes a `_folder.http` inside it, and that is not optional: **git does
// not track an empty directory**, so a folder created without a file in it
// would vanish the moment anyone cloned or checked out the branch — the
// collection would differ between two people for no visible reason. The
// Postman importer already does this for the same reason (docs/FORMAT.md §7,
// "An otherwise empty folder gets a _folder.http with a comment so the
// directory exists").
//
// The file it writes is a comment and nothing else: no auth, no headers, no
// variables. A new folder should inherit everything from above and declare
// nothing, so the comment explains what the file is for and the folder view is
// where you would add anything (DESIGN-NOTES §9.20).
//
// Like RequestService.Create, it does not touch `.order`: an unlisted
// directory sorts alphabetically after the listed entries (§2.2).
func (s *FolderService) Create(parentPath, name string) (string, error) {
	loaded, parent, err := s.folder(parentPath)
	if err != nil {
		return "", err
	}

	used := map[string]bool{}
	entries, err := os.ReadDir(parent.Path)
	if err != nil {
		return "", fmt.Errorf("creating a folder in %s: %w", displayPath(parentPath), err)
	}
	for _, entry := range entries {
		used[strings.TrimSuffix(entry.Name(), collection.RequestExt)] = true
	}
	base := collection.UniqueName(used, collection.Slug(name), "folder")

	dir := filepath.Join(parent.Path, base)
	settings := filepath.Join(dir, collection.FolderFileName)

	// Both writes inside the guard, like every write Otis makes: the watcher
	// would otherwise report the directory and the file as two separate
	// changes somebody else made, and the window would re-walk twice.
	release := s.collections.Guard().Writing(settings)
	if err := os.Mkdir(dir, 0o755); err != nil {
		release()
		return "", fmt.Errorf("creating %s: %w", path.Join(parentPath, base), err)
	}
	err = writeFileAtomic(settings, []byte(newFolderText(name)))
	release()
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", path.Join(parentPath, base, collection.FolderFileName), err)
	}

	if err := s.collections.Refresh(); err != nil {
		return "", err
	}
	_ = loaded
	return path.Join(parentPath, base), nil
}

// newFolderText is the `_folder.http` a new folder starts with: a comment
// saying what the file is, and nothing that changes behaviour.
func newFolderText(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "New folder"
	}
	return fmt.Sprintf(
		"# %s\n#\n# Shared settings for this folder. Headers, @auth and @name = value\n"+
			"# declared here apply to every request in it and below it.\n",
		trimmed)
}

// SaveReadme writes a folder's README.md.
func (s *FolderService) SaveReadme(nodePath, envName, text string) (FolderDocument, error) {
	_, folder, err := s.folder(nodePath)
	if err != nil {
		return FolderDocument{}, err
	}
	target := filepath.Join(folder.Path, ReadmeName)
	release := s.collections.Guard().Writing(target)
	err = writeFileAtomic(target, []byte(text))
	release()
	if err != nil {
		return FolderDocument{}, fmt.Errorf("saving %s: %w", path.Join(folder.ID, ReadmeName), err)
	}
	// A README is not part of the tree, so a re-walk is not needed for the
	// sidebar — but its git status is on the status bar, and Refresh is what
	// re-reads that.
	if err := s.collections.Refresh(); err != nil {
		return FolderDocument{}, err
	}
	return s.Load(nodePath, envName)
}

// ClearSession forgets the variables a run set for this folder.
func (s *FolderService) ClearSession(nodePath string) (FolderDocument, error) {
	if s.sessions == nil {
		return FolderDocument{}, errors.New("no session store")
	}
	if _, _, err := s.folder(nodePath); err != nil {
		return FolderDocument{}, err
	}
	if err := s.sessions.ClearSessionScope(resolve.SessionFolder, nodePath); err != nil {
		return FolderDocument{}, err
	}
	return s.Load(nodePath, "")
}

// readReadme reads the folder's README.md if there is one.
func (s *FolderService) readReadme(doc *FolderDocument, folder *collection.Node) {
	target := filepath.Join(folder.Path, ReadmeName)
	data, err := os.ReadFile(target)
	if err != nil {
		return
	}
	doc.ReadmePath = path.Join(folder.ID, ReadmeName)
	doc.Readme = string(data)
}

// describeCounts fills in the direct and recursive counts.
func (s *FolderService) describeCounts(doc *FolderDocument, folder *collection.Node) {
	for _, child := range folder.Children {
		switch child.Kind {
		case collection.KindFolder:
			doc.Counts.Subfolders++
		case collection.KindRequest:
			doc.Counts.Requests++
		case collection.KindScript:
			doc.Counts.Scripts++
		}
	}
	walk(folder, func(n *collection.Node) bool {
		if n.Kind == collection.KindRequest {
			doc.Counts.Below++
			doc.Inheriting = append(doc.Inheriting, n.ID)
		}
		return true
	})
	if doc.Inheriting == nil {
		doc.Inheriting = []string{}
	}
}

// describeScripts lists the folder's own scripts, hooks first.
func (s *FolderService) describeScripts(doc *FolderDocument, folder *collection.Node) {
	doc.Scripts = []FolderScript{}
	for _, child := range folder.Children {
		if child.Kind != collection.KindScript {
			continue
		}
		script := FolderScript{Path: child.ID, Name: child.Name}
		switch {
		case child.Name == collection.PreHookName:
			script.Hook = "pre"
		case child.Name == collection.PostHookName:
			script.Hook = "post"
		case child.Hook && strings.HasSuffix(child.Name, collection.PreHookSuffix):
			script.Hook = "pre"
		case child.Hook && strings.HasSuffix(child.Name, collection.PostHookSuffix):
			script.Hook = "post"
		}
		if data, err := os.ReadFile(child.Path); err != nil {
			script.Error = err.Error()
		} else {
			script.Source = string(data)
			script.Lines = countLines(script.Source)
		}
		doc.Scripts = append(doc.Scripts, script)
	}
	// Hooks before modules, then by name: the panel groups them that way and
	// the two mean different things.
	sort.SliceStable(doc.Scripts, func(i, j int) bool {
		a, b := doc.Scripts[i], doc.Scripts[j]
		if (a.Hook != "") != (b.Hook != "") {
			return a.Hook != ""
		}
		return a.Name < b.Name
	})
}

// describeSettings fills in the auth, headers and variables every request
// below the folder starts with, each with its provenance.
func (s *FolderService) describeSettings(doc *FolderDocument, loaded *collection.Collection, folder *collection.Node, envName string) {
	doc.Headers = []FolderHeader{}
	doc.Variables = []FolderVariable{}

	levels := resolve.FolderLevels(folder)
	own := path.Join(folder.ID, collection.FolderFileName)

	eff, err := resolve.Levels(levels)
	if err != nil {
		// A malformed @auth above is an error (§3.3). The folder still opens
		// and the panel says what is wrong.
		doc.Auth = &FolderAuth{Error: err.Error()}
	} else if eff != nil {
		for _, h := range eff.Headers {
			doc.Headers = append(doc.Headers, FolderHeader{
				Name: h.Name, Value: h.Value, Source: h.Source, Local: h.Source.Path == own,
			})
		}
		if eff.Auth != nil {
			doc.Auth = describeFolderAuth(eff.Auth, own)
		}
	}

	// Committed variables: this folder's and its ancestors', nearest wins.
	seen := map[string]bool{}
	for i := len(levels) - 1; i >= 0; i-- {
		lvl := levels[i]
		for _, entry := range lvl.Entries {
			for _, v := range entry.Variables {
				if seen[v.Name] {
					continue
				}
				seen[v.Name] = true
				doc.Variables = append(doc.Variables, FolderVariable{
					Name:   v.Name,
					Value:  v.Value,
					Source: resolve.Source{Path: lvl.Path, Line: v.Line},
					Local:  lvl.Path == own,
				})
			}
		}
	}
	sort.SliceStable(doc.Variables, func(i, j int) bool { return doc.Variables[i].Name < doc.Variables[j].Name })

	// Resolve the folder's own values, so the panels can style each
	// {{reference}} by what it actually resolves to and the auth can say
	// whether its token is a secret. Against secrets.Placeholder, never a
	// real store: this answers a binding call.
	doc.References = s.describeReferences(doc, loaded, levels, envName)
	if doc.Auth != nil {
		for _, ref := range doc.References {
			if ref.Secret && strings.Contains(doc.Auth.Token, "{{"+ref.Name) {
				doc.Auth.Secret = true
			}
		}
	}
}

// describeReferences resolves every {{name}} the folder's own values use.
func (s *FolderService) describeReferences(
	doc *FolderDocument,
	loaded *collection.Collection,
	levels []resolve.Level,
	envName string,
) []VariableRef {
	var env *resolve.Environment
	if envName != "" {
		env, _ = resolve.LoadEnvironment(loaded.Dir, envName)
	}
	scope := resolve.NewScope(levels, env, secrets.Placeholder{}, resolve.CollectionKey(loaded))
	x := resolve.NewExpander(scope)

	texts := make([]string, 0, len(doc.Headers)+len(doc.Variables)+8)
	for _, h := range doc.Headers {
		texts = append(texts, h.Value)
	}
	for _, v := range doc.Variables {
		texts = append(texts, v.Value)
	}
	if a := doc.Auth; a != nil {
		texts = append(texts, a.Token, a.Username, a.Profile, a.Region, a.Service)
	}
	for _, text := range texts {
		if text == "" {
			continue
		}
		if _, err := x.Expand(text); err != nil {
			// A cycle or a missing name stops this text; the names found so
			// far are still worth styling.
			continue
		}
	}

	refs := make([]VariableRef, 0, len(x.Uses()))
	for _, u := range x.Uses() {
		ref := VariableRef{
			Name: u.Name, Origin: u.Origin, Source: u.Source,
			Secret: u.Secret, Resolved: true,
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
	return refs
}

// describeOverrides finds the descendants that do not take one of the
// folder's settings.
func (s *FolderService) describeOverrides(doc *FolderDocument, folder *collection.Node) {
	doc.Overrides = []FolderOverride{}
	own := path.Join(folder.ID, collection.FolderFileName)
	inherited := map[string]bool{}
	for _, h := range doc.Headers {
		inherited[strings.ToLower(h.Name)] = true
	}

	walk(folder, func(n *collection.Node) bool {
		if n.Kind != collection.KindRequest || n.Request == nil {
			return true
		}
		levels := resolve.LevelsOf(n)

		// Auth: an override is a nearer @auth than the folder's.
		if doc.Auth != nil && doc.Auth.Kind != "" {
			if eff, err := resolve.Levels(levels); err == nil && eff != nil && eff.Auth != nil {
				if eff.Auth.Source != doc.Auth.Source {
					how := "sets its own " + string(eff.Auth.Kind)
					if eff.Auth.Kind == resolve.AuthNone {
						how = "uses none"
					}
					doc.Overrides = append(doc.Overrides, FolderOverride{
						Path: n.ID, Name: n.Name, What: "auth", How: how, Source: eff.Auth.Source,
					})
				}
			}
		}

		// Headers: a name this folder offers that the request overrode or
		// switched off.
		for _, row := range resolve.InheritedHeaders(levels) {
			if row.State == resolve.HeaderSent || row.Source.Path != own {
				continue
			}
			how := "sets its own"
			if row.State == resolve.HeaderOff {
				how = "switched off with " + resolve.InheritMarker
			}
			at := resolve.Source{}
			if row.By != nil {
				at = *row.By
			}
			doc.Overrides = append(doc.Overrides, FolderOverride{
				Path: n.ID, Name: n.Name, What: row.Name, How: how, Source: at,
			})
		}
		return true
	})
}

// describeFolderAuth summarises an effective auth for the panel. No secret
// value is read: the token is the {{reference}} as written.
func describeFolderAuth(auth *resolve.Auth, own string) *FolderAuth {
	out := &FolderAuth{
		Kind:     auth.Kind,
		Token:    auth.Token,
		Username: auth.Username,
		Profile:  auth.Profile,
		Region:   auth.Region,
		Service:  auth.Service,
		Source:   auth.Source,
		Local:    auth.Source.Path == own,
	}
	switch auth.Kind {
	case resolve.AuthBearer:
		out.Summary = "Bearer token"
		out.Sends = "Authorization: Bearer " + resolve.MaskPlaceholder
	case resolve.AuthBasic:
		out.Summary = "Basic"
		out.Sends = "Authorization: Basic " + resolve.MaskPlaceholder
	case resolve.AuthAWS:
		out.Summary = "AWS Signature v4"
		out.Sends = "Authorization: AWS4-HMAC-SHA256 " + resolve.MaskPlaceholder
	case resolve.AuthNone:
		out.Summary = "None"
		out.Sends = ""
	}
	return out
}

// folder resolves a node path to a folder in the cached walk.
func (s *FolderService) folder(nodePath string) (*collection.Collection, *collection.Node, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return nil, nil, err
	}
	node := loaded.Find(nodePath)
	if node == nil {
		return nil, nil, fmt.Errorf("%s is not in this collection", displayPath(nodePath))
	}
	if node.Kind != collection.KindFolder {
		return nil, nil, fmt.Errorf("%s is not a folder", displayPath(nodePath))
	}
	return loaded, node, nil
}

// displayPath names the root in an error message, which "" cannot.
func displayPath(nodePath string) string {
	if nodePath == "" {
		return "the collection root"
	}
	return nodePath
}

// walk visits a collection node and its descendants in display order.
func walk(n *collection.Node, fn func(*collection.Node) bool) bool {
	if !fn(n) {
		return false
	}
	for _, child := range n.Children {
		if !walk(child, fn) {
			return false
		}
	}
	return true
}

// countLines is the number of lines in a file, counting a final line without
// a terminator.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}
