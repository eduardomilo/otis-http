package httpclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
)

// Directives understood by Prepare (JetBrains names).
const (
	DirectiveNoRedirect  = "no-redirect"
	DirectiveNoCookieJar = "no-cookie-jar"
	DirectiveTimeout     = "timeout"
)

// Warning is a non-fatal note from Prepare.
type Warning string

// PrepareOptions configure Prepare.
type PrepareOptions struct {
	// AWS resolves credentials for "@auth aws" profiles. nil makes any
	// profile-based (non-static) AWS auth an error. Normally Session.AWS.
	AWS *AWSCredentials
	// Now supplies the signing time; nil means time.Now.
	Now func() time.Time
}

// Prepare turns a resolved request into a sendable one.
//
//   - @auth becomes an Authorization header unless the effective headers
//     already carry one, in which case the header wins and @auth is dropped.
//     AWS auth signs the request (Signature V4) after the body is known.
//   - A "< ./file" body is read relative to baseDir (the request file's
//     directory). The "<@" form additionally resolves {{variables}} in the
//     file content through res.Expand. A charset argument is not converted;
//     it produces a warning and the bytes are sent as-is.
//   - Directives: # @no-redirect, # @no-cookie-jar, # @timeout <seconds>.
func Prepare(ctx context.Context, res *resolve.Resolved, src *httpfile.Request, baseDir string, opts PrepareOptions) (*Request, []Warning, error) {
	req := &Request{Method: res.Method, URL: res.URL}
	var warnings []Warning

	hasAuthHeader := false
	for _, h := range res.Headers {
		if strings.EqualFold(h.Name, "Authorization") {
			hasAuthHeader = true
		}
		req.Headers = append(req.Headers, Header{Name: h.Name, Value: h.Value})
	}
	auth := res.Auth
	if hasAuthHeader {
		auth = nil
	}
	if auth != nil && auth.Kind != resolve.AuthAWS {
		if v, ok := authorizationValue(auth); ok {
			req.Headers = append(req.Headers, Header{Name: "Authorization", Value: v})
		}
	}

	switch {
	case res.Body.FilePath != "":
		path := res.Body.FilePath
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, filepath.FromSlash(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("body file %s: %w", res.Body.FilePath, err)
		}
		if res.Body.SubstituteVariables {
			expanded, err := res.Expand(string(data))
			if err != nil {
				return nil, nil, fmt.Errorf("body file %s: %w", res.Body.FilePath, err)
			}
			data = []byte(expanded)
		}
		if res.Body.Encoding != "" {
			warnings = append(warnings, Warning(fmt.Sprintf("body file %s: charset %q is not converted; bytes are sent as-is", res.Body.FilePath, res.Body.Encoding)))
		}
		req.Body = data
	case res.Body.Raw != "":
		req.Body = []byte(res.Body.Raw)
	}

	if auth != nil && auth.Kind == resolve.AuthAWS {
		now := time.Now()
		if opts.Now != nil {
			now = opts.Now()
		}
		if err := signAWS(ctx, req, auth, opts.AWS, now); err != nil {
			return nil, nil, err
		}
	}

	if src != nil {
		if _, ok := src.Directive(DirectiveNoRedirect); ok {
			req.Options.NoRedirect = true
		}
		if _, ok := src.Directive(DirectiveNoCookieJar); ok {
			req.Options.NoCookieJar = true
		}
		if v, ok := src.Directive(DirectiveTimeout); ok {
			secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || secs <= 0 {
				return nil, nil, fmt.Errorf("@timeout %q: expected a positive number of seconds", v)
			}
			req.Options.Timeout = time.Duration(secs * float64(time.Second))
		}
	}
	return req, warnings, nil
}

// authorizationValue renders an @auth as an Authorization header value.
// AuthNone yields no header.
func authorizationValue(a *resolve.Auth) (string, bool) {
	switch a.Kind {
	case resolve.AuthBearer:
		return "Bearer " + a.Token, true
	case resolve.AuthBasic:
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password)), true
	}
	return "", false
}
