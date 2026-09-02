// Package httpclient sends fully resolved requests and records what came
// back: status, headers, body, timing and the redirect chain.
//
// Prepare turns a resolve.Resolved into a Request (auth header, body from
// file, per-request directives); Client.Do executes one. A Session holds the
// cookie jar shared by the requests of one collection.
package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"time"
)

// Defaults.
const (
	DefaultTimeout      = 30 * time.Second
	DefaultMaxRedirects = 10
)

// Header is one request header in send order.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Request is ready to send: every variable resolved, auth turned into a
// header, body bytes in memory.
type Request struct {
	Method  string
	URL     string
	Headers []Header
	Body    []byte
	Options Options
}

// Options are per-request overrides, normally filled from directives.
type Options struct {
	// Timeout for the whole exchange including redirects; 0 uses the
	// client's default.
	Timeout time.Duration
	// NoRedirect returns the first 3xx response instead of following it.
	NoRedirect bool
	// NoCookieJar sends and stores no cookies for this request.
	NoCookieJar bool
}

// Redirect is one followed hop.
type Redirect struct {
	// URL is the address that answered with a redirect.
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode"`
	// Location is where it pointed.
	Location string `json:"location"`
}

// Timing breaks down the exchange. Zero values mean "not observed" (for
// example DNS and Connect on a reused connection). Values describe the last
// hop when redirects were followed.
type Timing struct {
	DNS     time.Duration `json:"dns"`
	Connect time.Duration `json:"connect"`
	TLS     time.Duration `json:"tls"`
	// TTFB is from the start of the request to the first response byte.
	TTFB time.Duration `json:"ttfb"`
	// Total is the full wall-clock time including reading the body.
	Total time.Duration `json:"total"`
}

// Response is the result of Do.
type Response struct {
	Status     string      `json:"status"`
	StatusCode int         `json:"statusCode"`
	Proto      string      `json:"proto"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"-"`
	// Size is len(Body).
	Size int64 `json:"size"`
	// Duration is the total wall-clock time (same as Timing.Total).
	Duration time.Duration `json:"duration"`
	Timing   Timing        `json:"timing"`
	// Redirects lists followed hops in order; empty when none.
	Redirects []Redirect `json:"redirects,omitempty"`
	// FinalURL is the URL that produced the returned response.
	FinalURL string `json:"finalUrl"`
}

// IsError reports a 4xx or 5xx status.
func (r *Response) IsError() bool { return r.StatusCode >= 400 }

// Session is the state shared by the requests of one collection: the cookie
// jar and the AWS credential cache. It lives in memory only.
type Session struct {
	Jar http.CookieJar
	AWS *AWSCredentials
}

// NewSession creates a session with an empty cookie jar and AWS cache.
func NewSession() *Session {
	jar, _ := cookiejar.New(nil) // only errors on a bad options value
	return &Session{Jar: jar, AWS: NewAWSCredentials()}
}

// PrepareOptions returns the options that bind Prepare to this session.
func (s *Session) PrepareOptions() PrepareOptions {
	if s == nil {
		return PrepareOptions{}
	}
	return PrepareOptions{AWS: s.AWS}
}

// Client sends requests. The zero value is usable with defaults.
type Client struct {
	// Timeout is the default per-request timeout (DefaultTimeout if 0).
	Timeout time.Duration
	// MaxRedirects bounds followed hops (DefaultMaxRedirects if 0).
	MaxRedirects int
	// Session supplies the cookie jar; nil means no cookies are kept.
	Session *Session
	// Transport overrides the round tripper (tests, proxies, TLS options).
	Transport http.RoundTripper
	// InsecureSkipVerify disables TLS certificate checks. Off by default.
	InsecureSkipVerify bool
}

// ErrTooManyRedirects is returned when a chain exceeds MaxRedirects.
var ErrTooManyRedirects = errors.New("too many redirects")

func (c *Client) transport() http.RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	if c.InsecureSkipVerify {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit user opt-in
	}
	return t
}

// Do sends req and reads the whole response body.
func (c *Client) Do(ctx context.Context, req *Request) (*Response, error) {
	timeout := req.Options.Timeout
	if timeout == 0 {
		timeout = c.Timeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	maxRedirects := c.MaxRedirects
	if maxRedirects == 0 {
		maxRedirects = DefaultMaxRedirects
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if bodyReader != nil {
		hreq.ContentLength = int64(len(req.Body))
	}
	for _, h := range req.Headers {
		if h.Name == "Host" || h.Name == "host" {
			hreq.Host = h.Value
			continue
		}
		hreq.Header.Add(h.Name, h.Value)
	}

	resp := &Response{}
	var trace traceState
	hreq = hreq.WithContext(httptrace.WithClientTrace(hreq.Context(), trace.clientTrace()))

	hc := &http.Client{
		Transport: c.transport(),
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			prev := via[len(via)-1]
			resp.Redirects = append(resp.Redirects, Redirect{
				URL:        prev.URL.String(),
				StatusCode: next.Response.StatusCode,
				Location:   next.URL.String(),
			})
			if req.Options.NoRedirect {
				resp.Redirects = nil
				return http.ErrUseLastResponse
			}
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: stopped after %d", ErrTooManyRedirects, maxRedirects)
			}
			return nil
		},
	}
	if c.Session != nil && !req.Options.NoCookieJar {
		hc.Jar = c.Session.Jar
	}

	start := time.Now()
	trace.start = start
	hresp, err := hc.Do(hreq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("request timed out after %s", timeout)
		}
		return nil, err
	}
	defer hresp.Body.Close()

	// Size the buffer from Content-Length when known so the body is read in
	// one allocation instead of growing repeatedly.
	var buf bytes.Buffer
	if hresp.ContentLength > 0 {
		buf.Grow(int(hresp.ContentLength))
	}
	if _, err := buf.ReadFrom(hresp.Body); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("request timed out after %s while reading the body", timeout)
		}
		return nil, fmt.Errorf("read body: %w", err)
	}
	total := time.Since(start)

	resp.Status = hresp.Status
	resp.StatusCode = hresp.StatusCode
	resp.Proto = hresp.Proto
	resp.Headers = hresp.Header
	resp.Body = buf.Bytes()
	resp.Size = int64(len(resp.Body))
	resp.Duration = total
	resp.Timing = trace.timing(total)
	resp.FinalURL = hresp.Request.URL.String()
	return resp, nil
}

// traceState collects httptrace callbacks.
type traceState struct {
	start                         time.Time
	dnsStart, connStart, tlsStart time.Time
	dns, connect, tlsDur, ttfb    time.Duration
}

func (t *traceState) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { t.dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { t.dns = time.Since(t.dnsStart) },
		ConnectStart:         func(string, string) { t.connStart = time.Now() },
		ConnectDone:          func(string, string, error) { t.connect = time.Since(t.connStart) },
		TLSHandshakeStart:    func() { t.tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { t.tlsDur = time.Since(t.tlsStart) },
		GotFirstResponseByte: func() { t.ttfb = time.Since(t.start) },
	}
}

func (t *traceState) timing(total time.Duration) Timing {
	return Timing{DNS: t.dns, Connect: t.connect, TLS: t.tlsDur, TTFB: t.ttfb, Total: total}
}
