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
	// AuthNone is an explicit opt-out: no auth is sent even if a folder
	// above defines one. It is distinct from Effective.Auth == nil, which
	// means nothing was declared anywhere.
	AuthNone AuthKind = "none"
)

// Auth is an effective @auth value with provenance. Token, Username and
// Password may still contain {{variables}}.
type Auth struct {
	Kind     AuthKind `json:"kind"`
	Token    string   `json:"token,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Source   Source   `json:"source"`
}

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
//	none
func parseAuth(value string, src Source) (*Auth, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil, &Error{src, "@auth needs a scheme: bearer <token>, basic <user> <password>, or none"}
	}
	scheme := strings.ToLower(fields[0])
	args := fields[1:]
	switch AuthKind(scheme) {
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
	return nil, &Error{src, fmt.Sprintf("unknown @auth scheme %q: expected bearer, basic or none", fields[0])}
}
