package httpclient

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDoEchoesRequest(t *testing.T) {
	var got struct {
		method, path, body, ct, host string
		xs                           []string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.body = r.Method, r.URL.RequestURI(), string(b)
		got.ct, got.host, got.xs = r.Header.Get("Content-Type"), r.Host, r.Header.Values("X-Multi")
		w.Header().Set("X-Reply", "yes")
		w.WriteHeader(201)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := &Client{}
	resp, err := c.Do(context.Background(), &Request{
		Method: "POST", URL: srv.URL + "/things?x=1",
		Headers: []Header{{"Content-Type", "application/json"}, {"X-Multi", "a"}, {"X-Multi", "b"}, {"Host", "custom.example"}},
		Body:    []byte(`{"a":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/things?x=1" || got.body != `{"a":1}` || got.ct != "application/json" || got.host != "custom.example" {
		t.Errorf("server saw %+v", got)
	}
	if len(got.xs) != 2 || got.xs[0] != "a" || got.xs[1] != "b" {
		t.Errorf("duplicate headers = %v", got.xs)
	}
	if resp.StatusCode != 201 || resp.Status != "201 Created" || string(resp.Body) != `{"ok":true}` || resp.Size != 11 {
		t.Errorf("resp = %+v body=%q", resp, resp.Body)
	}
	if resp.Headers.Get("X-Reply") != "yes" || resp.FinalURL != srv.URL+"/things?x=1" || resp.Proto != "HTTP/1.1" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Duration <= 0 || resp.Timing.Total != resp.Duration || resp.Timing.TTFB <= 0 || resp.Timing.Connect <= 0 {
		t.Errorf("timing = %+v", resp.Timing)
	}
	if resp.IsError() {
		t.Error("201 reported as error")
	}
	if len(resp.Redirects) != 0 {
		t.Errorf("redirects = %v", resp.Redirects)
	}
}

func TestRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", 301) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/c", 302) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "end") })
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", 302) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("followed and recorded", func(t *testing.T) {
		resp, err := (&Client{}).Do(context.Background(), &Request{Method: "GET", URL: srv.URL + "/a"})
		if err != nil {
			t.Fatal(err)
		}
		want := []Redirect{
			{URL: srv.URL + "/a", StatusCode: 301, Location: srv.URL + "/b"},
			{URL: srv.URL + "/b", StatusCode: 302, Location: srv.URL + "/c"},
		}
		if len(resp.Redirects) != 2 || resp.Redirects[0] != want[0] || resp.Redirects[1] != want[1] {
			t.Errorf("redirects = %+v, want %+v", resp.Redirects, want)
		}
		if resp.StatusCode != 200 || string(resp.Body) != "end" || resp.FinalURL != srv.URL+"/c" {
			t.Errorf("resp = %+v", resp)
		}
	})
	t.Run("no-redirect returns the 3xx", func(t *testing.T) {
		resp, err := (&Client{}).Do(context.Background(), &Request{Method: "GET", URL: srv.URL + "/a", Options: Options{NoRedirect: true}})
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 301 || resp.Headers.Get("Location") != "/b" || len(resp.Redirects) != 0 || resp.FinalURL != srv.URL+"/a" {
			t.Errorf("resp = %+v", resp)
		}
	})
	t.Run("too many", func(t *testing.T) {
		_, err := (&Client{MaxRedirects: 3}).Do(context.Background(), &Request{Method: "GET", URL: srv.URL + "/loop"})
		if !errors.Is(err, ErrTooManyRedirects) || !strings.Contains(err.Error(), "stopped after 3") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCookieJar(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "42", Path: "/"})
	})
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sid"); err == nil {
			fmt.Fprint(w, c.Value)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	get := func(c *Client, opts Options) string {
		resp, err := c.Do(context.Background(), &Request{Method: "GET", URL: srv.URL + "/get", Options: opts})
		if err != nil {
			t.Fatal(err)
		}
		return string(resp.Body)
	}
	session := &Client{Session: NewSession()}
	if _, err := session.Do(context.Background(), &Request{Method: "GET", URL: srv.URL + "/set"}); err != nil {
		t.Fatal(err)
	}
	if got := get(session, Options{}); got != "42" {
		t.Errorf("cookie not kept in session: %q", got)
	}
	if got := get(session, Options{NoCookieJar: true}); got != "" {
		t.Errorf("no-cookie-jar still sent cookie: %q", got)
	}
	if got := get(&Client{Session: NewSession()}, Options{}); got != "" {
		t.Errorf("cookie leaked across sessions: %q", got)
	}
	if got := get(&Client{}, Options{}); got != "" {
		t.Errorf("client without session sent cookie: %q", got)
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	t.Run("client default", func(t *testing.T) {
		_, err := (&Client{Timeout: 50 * time.Millisecond}).Do(context.Background(), &Request{Method: "GET", URL: srv.URL})
		if err == nil || !strings.Contains(err.Error(), "timed out after 50ms") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("request override", func(t *testing.T) {
		_, err := (&Client{Timeout: time.Minute}).Do(context.Background(), &Request{Method: "GET", URL: srv.URL, Options: Options{Timeout: 50 * time.Millisecond}})
		if err == nil || !strings.Contains(err.Error(), "timed out after 50ms") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestErrors(t *testing.T) {
	if _, err := (&Client{}).Do(context.Background(), &Request{Method: "GET", URL: "http://127.0.0.1:1/nope"}); err == nil {
		t.Error("expected connection error")
	}
	if _, err := (&Client{}).Do(context.Background(), &Request{Method: "BAD METHOD", URL: "http://x"}); err == nil || !strings.Contains(err.Error(), "build request") {
		t.Errorf("err = %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	resp, err := (&Client{}).Do(context.Background(), &Request{Method: "GET", URL: srv.URL})
	if err != nil || !resp.IsError() {
		t.Errorf("5xx is a response, not an error: %v %+v", err, resp)
	}
}

// TestLargeBody sends and receives 10 MB and checks the body buffer was
// sized from Content-Length rather than grown repeatedly.
func TestLargeBody(t *testing.T) {
	const size = 10 << 20
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				in, _ := io.ReadAll(r.Body)
				if len(in) != size || r.ContentLength != size {
					t.Errorf("server received %d bytes, Content-Length %d", len(in), r.ContentLength)
				}
				if !chunked {
					w.Header().Set("Content-Length", fmt.Sprint(size))
				}
				w.Write(in) //nolint:errcheck
			}))
			defer srv.Close()

			resp, err := (&Client{}).Do(context.Background(), &Request{Method: "POST", URL: srv.URL, Body: payload})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Size != size || len(resp.Body) != size || string(resp.Body[:64]) != string(payload[:64]) || string(resp.Body[size-64:]) != string(payload[size-64:]) {
				t.Errorf("size = %d, body mismatch", resp.Size)
			}
			if !chunked && cap(resp.Body) > size+4096 {
				t.Errorf("body buffer over-allocated: cap %d for %d bytes", cap(resp.Body), size)
			}
		})
	}
}
