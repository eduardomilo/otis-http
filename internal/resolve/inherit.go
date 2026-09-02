// Package resolve computes what a request will actually send: the headers
// and auth it inherits from _folder.http files (this file) and, in a later
// increment, the values of {{variables}}.
//
// Semantics are specified in docs/FORMAT.md §3. Every resolved value carries
// provenance (which file and line it came from) so the UI can explain it.
package resolve

import (
	"fmt"
	"path"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
)

// InheritMarker is the header value that removes an inherited header.
const InheritMarker = "!inherit"

// AuthDirective is the directive name carrying authentication.
const AuthDirective = "auth"

// Source identifies where a resolved value was defined.
type Source struct {
	// Path is relative to the collection root, e.g. "users/_folder.http"
	// or "users/create.http".
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

func (s Source) String() string {
	if s.Line == 0 {
		return s.Path
	}
	return fmt.Sprintf("%s:%d", s.Path, s.Line)
}

// Header is an effective header with provenance.
type Header struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source Source `json:"source"`
}

// Disabled records a header removed by "!inherit".
type Disabled struct {
	Name string `json:"name"`
	// Source is the "!inherit" line.
	Source Source `json:"source"`
	// Removed are the inherited headers that the marker suppressed. It is
	// empty when nothing above defined the header.
	Removed []Header `json:"removed,omitempty"`
}

// AuthKind is the scheme of an @auth directive.
type AuthKind string

const (
	AuthBearer AuthKind = "bearer"
	AuthBasic  AuthKind = "basic"
	// AuthAWS signs the request with AWS Signature Version 4.
	AuthAWS AuthKind = "aws"
	// AuthNone is an explicit opt-out: no auth is sent even if a folder
	// above defines one. It is distinct from Effective.Auth == nil, which
	// means nothing was declared anywhere.
	AuthNone AuthKind = "none"
)

// Auth is an effective @auth value with provenance. String fields may still
// contain {{variables}}.
type Auth struct {
	Kind     AuthKind `json:"kind"`
	Token    string   `json:"token,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`

	// AWS fields (Kind == AuthAWS). Either Profile or AccessKey/SecretKey
	// is set, never both. Region and Service are optional here; the sender
	// derives them from the profile and the host when empty.
	Profile      string `json:"profile,omitempty"`
	AccessKey    string `json:"accessKey,omitempty"`
	SecretKey    string `json:"-"`
	SessionToken string `json:"-"`
	Region       string `json:"region,omitempty"`
	Service      string `json:"service,omitempty"`

	Source Source `json:"source"`
}

// awsKeys are the accepted "key=value" names of "@auth aws".
var awsKeys = map[string]bool{"profile": true, "key": true, "secret": true, "token": true, "region": true, "service": true}

// Effective is the result of walking _folder.http files from the root down
// to a request.
type Effective struct {
	// Headers in send order: outermost folder first, request last. A header
	// overridden at a nearer level appears once, at the nearer level's
	// position.
	Headers []Header `json:"headers"`
	// Disabled lists "!inherit" markers encountered, nearest last.
	Disabled []Disabled `json:"disabled,omitempty"`
	// Auth is the nearest @auth directive, or nil if none was declared.
	Auth *Auth `json:"auth,omitempty"`
}

// Header returns the effective header with the given name (case-insensitive).
// If the name appears several times (duplicates at one level) the first is
// returned.
func (e *Effective) Header(name string) (Header, bool) {
	for _, h := range e.Headers {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
	}
	return Header{}, false
}

// Level is one step of the inheritance chain: a _folder.http or the request
// file itself.
type Level struct {
	// Path is relative to the collection root.
	Path string
	// Entries are the parsed entries whose headers and directives apply.
	Entries []*httpfile.Request
}

// Error is an inheritance error with provenance.
type Error struct {
	Source Source
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Source, e.Msg) }

// Inheritance computes the effective headers and auth of a request node by
// walking its folders from the collection root down to the request.
func Inheritance(node *collection.Node) (*Effective, error) {
	if node == nil || node.Kind != collection.KindRequest {
		return nil, fmt.Errorf("resolve: node is not a request")
	}
	if node.Request == nil {
		return nil, fmt.Errorf("resolve: %s: file has no request", node.ID)
	}
	return Levels(LevelsOf(node))
}

// LevelsOf builds the inheritance chain for a request node: every ancestor
// folder that has settings, root first, then the request itself.
func LevelsOf(node *collection.Node) []Level {
	anc := node.Ancestors() // nearest first
	levels := make([]Level, 0, len(anc)+1)
	for i := len(anc) - 1; i >= 0; i-- {
		folder := anc[i]
		if folder.Settings == nil {
			continue
		}
		levels = append(levels, Level{
			Path:    path.Join(folder.ID, collection.FolderFileName),
			Entries: folder.Settings.Requests,
		})
	}
	levels = append(levels, Level{Path: node.ID, Entries: []*httpfile.Request{node.Request}})
	return levels
}

// Levels resolves an explicit chain, outermost first. It is the core of
// Inheritance and is exposed for callers that build chains themselves.
func Levels(levels []Level) (*Effective, error) {
	eff := &Effective{Headers: []Header{}}
	for _, lvl := range levels {
		if err := eff.apply(lvl); err != nil {
			return nil, err
		}
	}
	return eff, nil
}

// apply merges one level into eff. Within a level, first every header name
// the level mentions (including "!inherit" markers) removes the inherited
// headers of that name; then the level's own non-marker headers are
// appended in file order. Duplicates within one level are all kept.
func (eff *Effective) apply(lvl Level) error {
	type named struct {
		h      httpfile.Header
		marker bool
	}
	var own []named
	for _, entry := range lvl.Entries {
		for _, h := range entry.Headers {
			own = append(own, named{h: h, marker: strings.TrimSpace(h.Value) == InheritMarker})
		}
	}
	// Pass 1: drop inherited headers this level redefines or disables.
	for _, n := range own {
		removed := eff.removeHeader(n.h.Name)
		if n.marker {
			eff.Disabled = append(eff.Disabled, Disabled{
				Name:    n.h.Name,
				Source:  Source{Path: lvl.Path, Line: n.h.Line},
				Removed: removed,
			})
		}
	}
	// Pass 2: append this level's headers.
	for _, n := range own {
		if n.marker {
			continue
		}
		eff.Headers = append(eff.Headers, Header{
			Name:   n.h.Name,
			Value:  n.h.Value,
			Source: Source{Path: lvl.Path, Line: n.h.Line},
		})
	}
	// Auth: nearest level wins; within a level the last directive wins.
	for _, entry := range lvl.Entries {
		for _, d := range entry.Directives {
			if d.Name != AuthDirective {
				continue
			}
			a, err := parseAuth(d.Value, Source{Path: lvl.Path, Line: d.Line})
			if err != nil {
				return err
			}
			eff.Auth = a
		}
	}
	return nil
}

// removeHeader deletes every effective header named name (case-insensitive)
// and returns the removed ones in order.
func (eff *Effective) removeHeader(name string) []Header {
	var removed, kept []Header
	for _, h := range eff.Headers {
		if strings.EqualFold(h.Name, name) {
			removed = append(removed, h)
		} else {
			kept = append(kept, h)
		}
	}
	if len(removed) > 0 {
		eff.Headers = kept
	}
	return removed
}

// parseAuth parses the value of an @auth directive:
//
//	bearer <token>
//	basic <username> [<password>]
//	aws [profile=<name>] [key=<id> secret=<key> [token=<session>]] [region=<r>] [service=<s>]
//	none
func parseAuth(value string, src Source) (*Auth, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil, &Error{src, "@auth needs a scheme: bearer <token>, basic <user> <password>, aws [key=value ...], or none"}
	}
	scheme := strings.ToLower(fields[0])
	args := fields[1:]
	switch AuthKind(scheme) {
	case AuthAWS:
		return parseAWSAuth(args, src)
	case AuthNone:
		if len(args) != 0 {
			return nil, &Error{src, fmt.Sprintf("@auth none takes no arguments, got %q", strings.Join(args, " "))}
		}
		return &Auth{Kind: AuthNone, Source: src}, nil
	case AuthBearer:
		if len(args) != 1 {
			return nil, &Error{src, fmt.Sprintf("@auth bearer takes exactly one token, got %d arguments", len(args))}
		}
		return &Auth{Kind: AuthBearer, Token: args[0], Source: src}, nil
	case AuthBasic:
		if len(args) == 0 {
			return nil, &Error{src, "@auth basic needs a username: basic <user> <password>"}
		}
		a := &Auth{Kind: AuthBasic, Username: args[0], Source: src}
		if len(args) > 1 {
			// The password is the rest of the line so it may contain spaces.
			rest := strings.TrimSpace(value)
			rest = strings.TrimSpace(rest[len(fields[0]):])
			rest = strings.TrimSpace(rest[len(args[0]):])
			a.Password = rest
		}
		return a, nil
	}
	return nil, &Error{src, fmt.Sprintf("unknown @auth scheme %q: expected bearer, basic, aws or none", fields[0])}
}

// parseAWSAuth parses the key=value arguments of "@auth aws".
func parseAWSAuth(args []string, src Source) (*Auth, error) {
	a := &Auth{Kind: AuthAWS, Source: src}
	seen := map[string]bool{}
	for _, arg := range args {
		k, v, ok := strings.Cut(arg, "=")
		k = strings.ToLower(k)
		if !ok || !awsKeys[k] {
			return nil, &Error{src, fmt.Sprintf("@auth aws: unexpected %q; expected key=value with one of profile, key, secret, token, region, service", arg)}
		}
		if v == "" {
			return nil, &Error{src, fmt.Sprintf("@auth aws: %s= must not be empty", k)}
		}
		if seen[k] {
			return nil, &Error{src, fmt.Sprintf("@auth aws: %s= given more than once", k)}
		}
		seen[k] = true
		switch k {
		case "profile":
			a.Profile = v
		case "key":
			a.AccessKey = v
		case "secret":
			a.SecretKey = v
		case "token":
			a.SessionToken = v
		case "region":
			a.Region = v
		case "service":
			a.Service = v
		}
	}
	switch {
	case a.Profile != "" && (a.AccessKey != "" || a.SecretKey != "" || a.SessionToken != ""):
		return nil, &Error{src, "@auth aws: profile= cannot be combined with key=, secret= or token="}
	case (a.AccessKey == "") != (a.SecretKey == ""):
		return nil, &Error{src, "@auth aws: key= and secret= must be given together"}
	case a.SessionToken != "" && a.AccessKey == "":
		return nil, &Error{src, "@auth aws: token= requires key= and secret="}
	}
	return a, nil
}

// HeaderState is the fate of a header an ancestor folder contributed.
type HeaderState string

const (
	// HeaderSent means the header is in the effective set and goes on the wire.
	HeaderSent HeaderState = "sent"
	// HeaderOverridden means a nearer level defined the same name, so this
	// value is replaced (docs/FORMAT.md §3.1: override is total).
	HeaderOverridden HeaderState = "overridden"
	// HeaderOff means a nearer level wrote "!inherit" for the name, so
	// nothing of it is sent (§3.2).
	HeaderOff HeaderState = "off"
)

// Inherited is one header an ancestor folder contributed, with what became
// of it. Effective.Headers lists only what is sent; this lists everything a
// folder above offered, so the UI can show an inherited header that was
// overridden or switched off — and switch it back on.
type Inherited struct {
	Name   string      `json:"name"`
	Value  string      `json:"value"`
	Source Source      `json:"source"`
	State  HeaderState `json:"state"`
	// By is the level that overrode or disabled the header. It is nil when
	// State is HeaderSent.
	By *Source `json:"by,omitempty"`
}

// InheritedHeaders reports every header contributed by a level other than the
// last one, with its fate. The chain is ordered outermost first, request last,
// exactly as Levels takes it.
//
// The last level is the request itself, so its headers are "local" rather than
// inherited; they appear here only as the cause of an override or an "!inherit".
func InheritedHeaders(levels []Level) []Inherited {
	if len(levels) == 0 {
		return nil
	}
	var out []Inherited
	for i, lvl := range levels {
		// Every name this level mentions settles the fate of the same name
		// above it, marker or not (§3.1, pass 1).
		for _, entry := range lvl.Entries {
			for _, h := range entry.Headers {
				src := Source{Path: lvl.Path, Line: h.Line}
				state := HeaderOverridden
				if strings.TrimSpace(h.Value) == InheritMarker {
					state = HeaderOff
				}
				for j := range out {
					if out[j].State == HeaderSent && strings.EqualFold(out[j].Name, h.Name) {
						out[j].State = state
						by := src
						out[j].By = &by
					}
				}
			}
		}
		if i == len(levels)-1 {
			break // the request's own headers are local, not inherited
		}
		for _, entry := range lvl.Entries {
			for _, h := range entry.Headers {
				if strings.TrimSpace(h.Value) == InheritMarker {
					continue // a marker sends nothing of its own (§3.2)
				}
				out = append(out, Inherited{
					Name:   h.Name,
					Value:  h.Value,
					Source: Source{Path: lvl.Path, Line: h.Line},
					State:  HeaderSent,
				})
			}
		}
	}
	return out
}

// AncestorAuth is the auth the folders above a request declare, ignoring the
// request's own @auth. It is what "Inherit from folder" shows and what
// "Override for this request" is prefilled from (screen 4b).
func AncestorAuth(levels []Level) *Auth {
	if len(levels) < 2 {
		return nil
	}
	eff, err := Levels(levels[:len(levels)-1])
	if err != nil || eff == nil {
		// A malformed @auth above is reported when the request is resolved
		// or sent; it must not stop the editor from opening.
		return nil
	}
	return eff.Auth
}

// FolderLevels builds the inheritance chain *at* a folder: every folder from
// the collection root down to and including this one that has settings.
//
// LevelsOf answers "what does this request end up with"; this answers "what
// does every request below this folder start with", which is the folder view's
// question (screen 3a: "Settings in orders/_folder.http · inherited by every
// request below"). The folder's own settings are the last level, so they win
// over its ancestors' exactly as they will for the requests underneath.
func FolderLevels(folder *collection.Node) []Level {
	if folder == nil || folder.Kind != collection.KindFolder {
		return nil
	}
	chain := append([]*collection.Node{folder}, folder.Ancestors()...) // nearest first
	levels := make([]Level, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		node := chain[i]
		if node.Settings == nil {
			continue
		}
		levels = append(levels, Level{
			Path:    path.Join(node.ID, collection.FolderFileName),
			Entries: node.Settings.Requests,
		})
	}
	return levels
}

// FolderInheritance is the effective headers and auth every request below
// folder starts with.
func FolderInheritance(folder *collection.Node) (*Effective, error) {
	return Levels(FolderLevels(folder))
}
