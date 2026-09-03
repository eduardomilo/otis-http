package resolve

import (
	"fmt"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/secrets"
)

// Options configure request resolution.
type Options struct {
	// Env is the active environment, or nil for none.
	Env *Environment
	// Secrets resolves {"$secret": "keychain"} references. nil means any
	// secret reference is an error.
	Secrets secrets.Store
	// Collection is the collection key used for secret lookups
	// (<collection>/<env>/<name>). Defaults to the collection directory's
	// base name when resolving through a collection.
	Collection string
	// Session supplies the variables a run set (§4.5). nil means the request
	// resolves against the files and the environment only, which is the CLI's
	// case and every case before the first run.
	Session *Session
	// Extra are values that sit above every other scope: the request-scope
	// variables a pre-request script set (docs/FORMAT.md §9.4). They live for
	// one send and are in no file, so they belong nowhere in the file-based
	// order — above all of it is the only honest place.
	Extra map[string]string
	// Rewrite, when set, is called after inheritance and before resolution
	// with the entry and its effective headers and auth, so a caller may
	// change them.
	//
	// It exists for exactly one caller: the pre-request scripts of §9.2, which
	// run before resolution so a folder header can reference a value a script
	// is about to set. A hook rather than a second entry point, because
	// everything else about resolution — the scope, the order, the masking —
	// has to stay identical whether a script ran or not.
	Rewrite func(*httpfile.Request, *Effective)
	// Scope hooks for deterministic tests; nil means real clock/randomness.
	Configure func(*Scope)
}

// Resolved is a request with inheritance applied and every variable
// substituted. It is what the sender consumes.
type Resolved struct {
	Method  string `json:"method"`
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
	// Headers are the effective headers (section 3) with values resolved.
	Headers []Header `json:"headers"`
	// Auth is the effective auth with arguments resolved, or nil.
	Auth *Auth `json:"auth,omitempty"`
	// Body has Raw resolved. FilePath is left as written; reading the file
	// (and, for the <@ form, resolving its content) is the sender's job.
	Body httpfile.Body `json:"body"`
	// Disabled carries over from inheritance for display.
	Disabled []Disabled `json:"disabled,omitempty"`
	// Variables lists every variable used, with provenance. Secret values
	// are not included.
	Variables []Use `json:"variables,omitempty"`

	// secretValues are the raw secret values used, kept unexported so they
	// never serialize; Mask uses them.
	secretValues []string
	// scope lets Expand resolve more text (a body loaded from a file) with
	// the same variables.
	scope *Scope
}

// Expand resolves {{variables}} in text against the same scope as the
// request, for content that becomes known only later, such as a body read
// from a file with the "<@" form. Secrets it uses are added to the set that
// Mask hides.
func (r *Resolved) Expand(text string) (string, error) {
	if r.scope == nil {
		return text, nil
	}
	x := NewExpander(r.scope)
	out, err := x.Expand(text)
	if err != nil {
		return "", err
	}
	if err := x.Err(); err != nil {
		return "", err
	}
	r.secretValues = append(r.secretValues, x.Secrets...)
	r.Variables = append(r.Variables, x.Uses()...)
	return out, nil
}

// HasSecrets reports whether any resolved value came from a secret.
func (r *Resolved) HasSecrets() bool { return len(r.secretValues) > 0 }

// AddSecret registers a value with this request's masker.
//
// It exists for the one secret resolution does not see: a value a script put
// on the request through secrets.ref (docs/FORMAT.md §9.7). The sender
// substitutes it into the prepared request, and everything Otis shows about
// that request goes through Mask — so registering it here is what keeps the
// window's account of the send masked as well as the script's.
func (r *Resolved) AddSecret(value string) {
	if value == "" {
		return
	}
	for _, existing := range r.secretValues {
		if existing == value {
			return
		}
	}
	r.secretValues = append(r.secretValues, value)
}

// Mask replaces every secret value used by this request with a
// placeholder. Anything derived from a Resolved that leaves Go must go
// through it.
func (r *Resolved) Mask(text string) string {
	x := &Expander{Secrets: r.secretValues}
	return x.Mask(text)
}

// Request resolves a request node: inheritance, then variables in the URL,
// header values, auth arguments and raw body. All unresolved variables are
// reported together in one *MissingError.
func Request(node *collection.Node, opts Options) (*Resolved, error) {
	if node == nil || node.Kind != collection.KindRequest || node.Request == nil {
		return nil, fmt.Errorf("resolve: node is not a request")
	}
	levels := LevelsOf(node)
	eff, err := Levels(levels)
	if err != nil {
		return nil, err
	}
	return resolveWith(node.Request, levels, eff, opts, node.ID)
}

// InCollection is Request with the collection key defaulted to the
// collection directory's base name.
func InCollection(c *collection.Collection, node *collection.Node, opts Options) (*Resolved, error) {
	if opts.Collection == "" {
		opts.Collection = CollectionKey(c)
	}
	return Request(node, opts)
}

// CollectionKey is the collection component of the key its secrets are stored
// under: the collection's display name (docs/FORMAT.md §5).
//
// The display name, not the raw base name, and the difference matters now that
// the store is a real machine-wide keychain. A collection kept beside the code
// it exercises is conventionally called ".requests", so the raw base name
// would key *every* such collection on the machine identically: project A's
// "staging/apiKey" and project B's would be one keychain entry, and opening B
// would silently resolve A's credential. The display name walks up out of a
// dot-directory, which is also what the design specifies — screen 1c shows the
// service string "acme-api/staging/apiKey" for a collection rooted at
// "~/code/acme-api/.requests".
//
// The cost is that renaming the directory the collection sits in moves its
// secrets, and they have to be set again. That is the smaller of the two
// problems, and it is visible when it happens; a silent collision between two
// projects is neither.
func CollectionKey(c *collection.Collection) string {
	return collection.DisplayName(c.Dir)
}

func resolveWith(req *httpfile.Request, levels []Level, eff *Effective, opts Options, requestID string) (*Resolved, error) {
	if opts.Rewrite != nil {
		opts.Rewrite(req, eff)
	}
	scope := NewScope(levels, opts.Env, opts.Secrets, opts.Collection)
	if opts.Session != nil {
		scope.WithSession(opts.Session, FolderChain(requestID))
	}
	if len(opts.Extra) > 0 {
		scope.WithExtra(opts.Extra)
	}
	if opts.Configure != nil {
		opts.Configure(scope)
	}
	x := NewExpander(scope)
	res := &Resolved{Method: req.Method, Version: req.Version, Disabled: eff.Disabled, Body: req.Body}

	var err error
	if res.URL, err = x.Expand(req.URL); err != nil {
		return nil, err
	}
	res.Headers = make([]Header, len(eff.Headers))
	for i, h := range eff.Headers {
		v, err := x.Expand(h.Value)
		if err != nil {
			return nil, err
		}
		res.Headers[i] = Header{Name: h.Name, Value: v, Source: h.Source}
	}
	if eff.Auth != nil {
		a := *eff.Auth
		for _, f := range []*string{&a.Token, &a.Username, &a.Password, &a.Profile, &a.AccessKey, &a.SecretKey, &a.SessionToken, &a.Region, &a.Service} {
			if *f, err = x.Expand(*f); err != nil {
				return nil, err
			}
		}
		// Explicit AWS secret material is always treated as secret for
		// masking, even when it came from a plain variable.
		for _, v := range []string{a.SecretKey, a.SessionToken} {
			if v != "" {
				x.Secrets = append(x.Secrets, v)
			}
		}
		res.Auth = &a
	}
	if res.Body.Raw, err = x.Expand(req.Body.Raw); err != nil {
		return nil, err
	}
	if err := x.Err(); err != nil {
		return nil, err
	}
	res.Variables = x.Uses()
	res.secretValues = x.Secrets
	res.scope = scope
	return res, nil
}
