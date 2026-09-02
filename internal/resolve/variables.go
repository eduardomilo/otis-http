package resolve

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/secrets"
)

// varRe matches {{name}} with optional inner whitespace. Anything else
// between braces is literal text.
var varRe = regexp.MustCompile(`\{\{\s*([A-Za-z_$][\w.\-]*)\s*\}\}`)

// Origin says which scope a variable value came from.
type Origin string

const (
	OriginRequest Origin = "request" // @var in the request file
	OriginFolder  Origin = "folder"  // @var in a _folder.http
	OriginEnv     Origin = "env"     // env/<name>.json
	OriginSession Origin = "session" // set by a run; in memory only (§4.5)
	OriginBuiltin Origin = "builtin" // {{$uuid}} and friends
)

// Use records one variable the resolver needed, with provenance. Secret
// values are never stored here.
type Use struct {
	Name   string `json:"name"`
	Origin Origin `json:"origin"`
	Source Source `json:"source"`
	// Value is the resolved value, or "" when Secret is true.
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// MissingError lists every unresolved variable, not just the first.
type MissingError struct {
	Names []string
}

func (e *MissingError) Error() string {
	return "unresolved variables: " + strings.Join(e.Names, ", ")
}

// CycleError reports a variable whose value refers back to itself.
type CycleError struct {
	Chain []string
}

func (e *CycleError) Error() string {
	return "variable cycle: " + strings.Join(e.Chain, " -> ")
}

// SecretError reports a secret reference that could not be satisfied. It
// never carries a value.
type SecretError struct {
	Name string
	Key  string
	Err  error
}

func (e *SecretError) Error() string {
	if e.Err == secrets.ErrNotFound {
		return fmt.Sprintf("secret %q (%s) is not set in the secret store", e.Name, e.Key)
	}
	return fmt.Sprintf("secret %q (%s): %v", e.Name, e.Key, e.Err)
}

func (e *SecretError) Unwrap() error { return e.Err }

// declared is a @var declaration with provenance.
type declared struct {
	name   string
	value  string
	origin Origin
	source Source
	// literal suppresses recursive expansion of the value. File declarations
	// are templates; a session value is data a run produced.
	literal bool
}

// Scope is the ordered set of variable definitions visible to a request.
type Scope struct {
	// vars are file declarations, nearest scope first; within a scope the
	// last declaration wins, so lookups scan each level from the end. Each
	// level records the folder it belongs to, so a session value set for that
	// folder can be consulted just before it.
	levels     []level
	env        *Environment
	store      secrets.Store
	collection string

	// session and folders drive the session scope (docs/FORMAT.md §4.5).
	// folders is nearest-folder-first and ends at the root, and it is passed
	// in rather than derived from levels: a folder with no _folder.http
	// contributes no level but can still hold a session value.
	session *Session
	folders []string
	envName string

	now     func() time.Time
	uuid    func() string
	randInt func(max int) int
}

// level is one file's declarations, with the folder that file sits in.
type level struct {
	folder string
	decls  []declared
}

// NewScope builds the variable scope for an inheritance chain (outermost
// first, request last), an optional environment and an optional secret
// store. collection is the collection key used for secret lookups.
func NewScope(levels []Level, env *Environment, store secrets.Store, collection string) *Scope {
	s := &Scope{env: env, store: store, collection: collection}
	if env != nil {
		s.envName = env.Name
	}
	for i := len(levels) - 1; i >= 0; i-- { // nearest first
		lvl := levels[i]
		origin := OriginFolder
		if i == len(levels)-1 {
			origin = OriginRequest
		}
		var ds []declared
		for _, entry := range lvl.Entries {
			for _, v := range entry.Variables {
				ds = append(ds, declaredFrom(v, origin, lvl.Path))
			}
		}
		if len(ds) > 0 {
			s.levels = append(s.levels, level{folder: folderOf(lvl.Path), decls: ds})
		}
	}
	return s
}

// folderOf is the folder a level's file sits in, as a node ID.
func folderOf(levelPath string) string {
	if i := strings.LastIndex(levelPath, "/"); i >= 0 {
		return levelPath[:i]
	}
	return ""
}

// WithSession attaches the session store and the folder chain it is consulted
// with, nearest folder first (see FolderChain).
func (s *Scope) WithSession(session *Session, folders []string) *Scope {
	s.session, s.folders = session, folders
	return s
}

// WithClock overrides the clock used by {{$timestamp}} and
// {{$isoTimestamp}} (for tests).
func (s *Scope) WithClock(now func() time.Time) *Scope { s.now = now; return s }

// WithRandom overrides the sources of {{$uuid}} and {{$randomInt}} (for
// tests). randInt must return a value in [0, max].
func (s *Scope) WithRandom(uuid func() string, randInt func(max int) int) *Scope {
	s.uuid, s.randInt = uuid, randInt
	return s
}

// lookup finds a name in the file and session scopes, nearest first. The
// environment and the builtins are handled by the expander: an environment
// value is literal rather than expanded further, and a builtin is evaluated
// per occurrence.
//
// The order within the folder scopes is the format's own rule applied to a new
// layer: nearest definition wins, and at one level a session value beats the
// committed one. Anything else would make vars.folder.set a no-op whenever the
// folder already declared the name, which is exactly when it is wanted.
func (s *Scope) lookup(name string) (declared, bool) {
	// The request file's own declarations come first and have no session
	// layer: a per-request session value lives only for one execution and is
	// the script engine's business (§4.5).
	next := 0
	if len(s.levels) > 0 && s.levels[0].isRequest() {
		if d, ok := s.levels[0].find(name); ok {
			return d, true
		}
		next = 1
	}
	// Then the folder chain, nearest first: session value, then that folder's
	// _folder.http declarations.
	for _, folder := range s.folders {
		if d, ok := s.sessionValue(SessionFolder, folder, name); ok {
			return d, true
		}
		for _, lvl := range s.levels[next:] {
			if lvl.folder == folder {
				if d, ok := lvl.find(name); ok {
					return d, true
				}
			}
		}
	}
	// A scope built without a folder chain (a caller that has no node, and
	// every test written before the session scope existed) still resolves its
	// file declarations in order.
	if len(s.folders) == 0 {
		for _, lvl := range s.levels[next:] {
			if d, ok := lvl.find(name); ok {
				return d, true
			}
		}
	}
	return declared{}, false
}

// isRequest reports whether the level is the request file rather than a
// _folder.http.
func (l level) isRequest() bool {
	return len(l.decls) > 0 && l.decls[0].origin == OriginRequest
}

// find returns the last declaration of name in the level; within one file the
// last declaration wins (docs/FORMAT.md §1.5).
func (l level) find(name string) (declared, bool) {
	for i := len(l.decls) - 1; i >= 0; i-- {
		if l.decls[i].name == name {
			return l.decls[i], true
		}
	}
	return declared{}, false
}

// sessionValue looks one session variable up as a declaration.
//
// Its value is returned literal, not expanded: it was produced by a run, not
// written by a person, so there is nothing in it a reader expected to be a
// template — and expanding it would let a value carrying "{{" from a server
// response reach into the variable scope.
func (s *Scope) sessionValue(scope SessionScope, owner, name string) (declared, bool) {
	if s.session == nil {
		return declared{}, false
	}
	v, ok := s.session.Get(scope, owner, name)
	if !ok {
		return declared{}, false
	}
	return declared{
		name:    name,
		value:   v.Value,
		origin:  OriginSession,
		source:  Source{Path: sessionSourceLabel(scope, owner)},
		literal: true,
	}, true
}

// sessionSourceLabel names where a session value lives, for provenance. It is
// not a file path — that is the point of it.
func sessionSourceLabel(scope SessionScope, owner string) string {
	switch scope {
	case SessionEnv:
		return "session:env/" + owner
	default:
		if owner == "" {
			return "session:/"
		}
		return "session:" + owner + "/"
	}
}

// Expander resolves {{variables}} in text against a Scope, accumulating
// uses and missing names across several Expand calls so that one request
// yields one error listing everything that is missing.
type Expander struct {
	scope   *Scope
	uses    []Use
	seen    map[string]bool
	missing []string
	// Secrets holds the raw secret values used so far, for masking.
	Secrets []string
}

// NewExpander creates an expander over scope.
func NewExpander(scope *Scope) *Expander {
	return &Expander{scope: scope, seen: map[string]bool{}}
}

// Uses returns every variable resolved so far, in first-use order.
func (x *Expander) Uses() []Use { return x.uses }

// Err returns a *MissingError if any variable was unresolved, else nil.
func (x *Expander) Err() error {
	if len(x.missing) == 0 {
		return nil
	}
	return &MissingError{Names: x.missing}
}

// Expand resolves every {{name}} in text. Unresolved names are recorded and
// left in place; cycle and secret-store errors are returned immediately.
func (x *Expander) Expand(text string) (string, error) {
	return x.expand(text, nil)
}

func (x *Expander) expand(text string, chain []string) (string, error) {
	var firstErr error
	out := varRe.ReplaceAllStringFunc(text, func(m string) string {
		if firstErr != nil {
			return m
		}
		name := varRe.FindStringSubmatch(m)[1]
		v, ok, err := x.value(name, chain)
		if err != nil {
			firstErr = err
			return m
		}
		if !ok {
			x.noteMissing(name)
			return m
		}
		return v
	})
	return out, firstErr
}

func (x *Expander) noteMissing(name string) {
	for _, n := range x.missing {
		if n == name {
			return
		}
	}
	x.missing = append(x.missing, name)
}

func (x *Expander) noteUse(u Use) { x.reserveUse(u) }

// reserveUse appends u unless the same name from the same origin was
// already recorded (builtins are always recorded). It returns the index of
// the appended use, or -1.
func (x *Expander) reserveUse(u Use) int {
	key := string(u.Origin) + "\x00" + u.Name
	if u.Origin != OriginBuiltin && x.seen[key] {
		return -1
	}
	x.seen[key] = true
	x.uses = append(x.uses, u)
	return len(x.uses) - 1
}

// value resolves one variable name. Builtins are evaluated fresh on every
// occurrence. File values are expanded recursively; environment and secret
// values are literal.
func (x *Expander) value(name string, chain []string) (string, bool, error) {
	if strings.HasPrefix(name, "$") {
		v, ok := x.builtin(name)
		if ok {
			x.noteUse(Use{Name: name, Origin: OriginBuiltin, Source: Source{Path: "builtin"}, Value: v})
		}
		return v, ok, nil
	}
	for _, c := range chain {
		if c == name {
			return "", false, &CycleError{Chain: append(append([]string{}, chain...), name)}
		}
	}
	if d, ok := x.scope.lookup(name); ok {
		// Reserve the slot before recursing so uses read in first-use order
		// (outer variable before the ones its value refers to).
		slot := x.reserveUse(Use{Name: name, Origin: d.origin, Source: d.source})
		if d.literal {
			// A session value is data a run produced, not a template somebody
			// wrote; "{{" arriving in a response must not reach into the
			// variable scope.
			if slot >= 0 {
				x.uses[slot].Value = d.value
			}
			return d.value, true, nil
		}
		// A file variable whose value refers to a secret resolves *to* that
		// secret: "@token = {{apiKey}}" is the secret with another name. So
		// the recursion is bracketed to see whether it consumed one, and the
		// use is marked secret and left without a value if it did. Without
		// this, Use.Value — which is serialized to the frontend and to
		// `otis run --json` — would carry the secret in the clear.
		before := len(x.Secrets)
		v, err := x.expand(d.value, append(chain, name))
		if err != nil {
			return "", false, err
		}
		if slot >= 0 {
			if len(x.Secrets) > before {
				x.uses[slot].Secret = true
			} else {
				x.uses[slot].Value = v
			}
		}
		return v, true, nil
	}
	// A session value set for the active environment beats the committed one,
	// for the same reason a folder session value beats the folder's own (§4.5).
	if x.scope.envName != "" {
		if d, ok := x.scope.sessionValue(SessionEnv, x.scope.envName, name); ok {
			x.noteUse(Use{Name: name, Origin: d.origin, Source: d.source, Value: d.value})
			return d.value, true, nil
		}
	}
	if x.scope.env != nil {
		if ev, ok := x.scope.env.Values[name]; ok {
			src := Source{Path: x.scope.env.Path}
			if !ev.Secret {
				x.noteUse(Use{Name: name, Origin: OriginEnv, Source: src, Value: ev.Value})
				return ev.Value, true, nil
			}
			key := secrets.Key(x.scope.collection, x.scope.env.Name, name)
			if x.scope.store == nil {
				return "", false, &SecretError{Name: name, Key: key, Err: fmt.Errorf("no secret store available")}
			}
			v, err := x.scope.store.Get(key)
			if err != nil {
				return "", false, &SecretError{Name: name, Key: key, Err: err}
			}
			x.noteUse(Use{Name: name, Origin: OriginEnv, Source: src, Secret: true})
			x.Secrets = append(x.Secrets, v)
			return v, true, nil
		}
	}
	return "", false, nil
}

func (x *Expander) builtin(name string) (string, bool) {
	s := x.scope
	switch name {
	case "$uuid":
		if s.uuid != nil {
			return s.uuid(), true
		}
		return newUUID(), true
	case "$timestamp":
		return strconv.FormatInt(s.clock().Unix(), 10), true
	case "$isoTimestamp":
		return s.clock().UTC().Format(time.RFC3339), true
	case "$randomInt":
		if s.randInt != nil {
			return strconv.Itoa(s.randInt(randomIntMax)), true
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(randomIntMax+1))
		return n.String(), true
	}
	return "", false
}

// randomIntMax is the inclusive upper bound of {{$randomInt}}, matching the
// VS Code REST Client.
const randomIntMax = 1000

func (s *Scope) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// newUUID returns a random (version 4) UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Mask replaces every secret value used so far with a placeholder. Use it
// before showing resolved text outside Go.
func (x *Expander) Mask(text string) string {
	// Longest first so that a secret that contains another is masked whole.
	vals := append([]string{}, x.Secrets...)
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		if v != "" {
			text = strings.ReplaceAll(text, v, MaskPlaceholder)
		}
	}
	return text
}

// MaskPlaceholder replaces secret values in masked output.
const MaskPlaceholder = "•••••"

func declaredFrom(v httpfile.Variable, origin Origin, path string) declared {
	return declared{name: v.Name, value: v.Value, origin: origin, source: Source{Path: path, Line: v.Line}}
}
