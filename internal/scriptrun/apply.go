package scriptrun

import (
	"net/http"
	"sort"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpclient"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/script"
)

// RequestView builds the view a pre-request script shapes. Every value is the
// template: resolution has not happened yet (docs/FORMAT.md §9.2).
func RequestView(node *collection.Node, entry *httpfile.Request, eff *resolve.Effective) *script.RequestView {
	view := &script.RequestView{
		Method: entry.Method, URL: entry.URL, Body: entry.Body.Raw,
		Path: node.ID, Name: node.Name,
	}
	for _, h := range eff.Headers {
		view.Headers = append(view.Headers, script.HeaderPair{Name: h.Name, Value: h.Value})
	}
	return view
}

// ApplyView folds a script's changes back into the entry and its effective
// headers, so resolution proceeds over what the script produced.
func ApplyView(entry *httpfile.Request, eff *resolve.Effective, view *script.RequestView) {
	entry.Method, entry.URL = view.Method, view.URL
	entry.Body.Raw = view.Body
	// The entry's own headers are cleared and the effective list replaced: a
	// script's headers are the last word before resolution (§9.5), so keeping
	// the file's alongside them would send both.
	entry.Headers = nil
	eff.Headers = eff.Headers[:0]
	for _, h := range view.Headers {
		eff.Headers = append(eff.Headers, resolve.Header{
			Name: h.Name, Value: h.Value,
			Source: resolve.Source{Path: "a pre-request script"},
		})
	}
}

// SentView is the request as it went, masked, for a post-response script.
func SentView(node *collection.Node, res *resolve.Resolved, req *httpclient.Request) *script.RequestView {
	view := &script.RequestView{
		Method: req.Method, URL: res.Mask(req.URL),
		Path: node.ID, Name: node.Name,
	}
	for _, h := range req.Headers {
		view.Headers = append(view.Headers, script.HeaderPair{Name: h.Name, Value: res.Mask(h.Value)})
	}
	return view
}

// SubstituteHandles replaces a script's secret masks in the prepared request
// with the real values, and registers each with the masker.
//
// By slot rather than by searching the text: the slot says which header or
// body the handle was put on, so a mask that happens to appear in an
// unrelated value is left alone.
func SubstituteHandles(res *resolve.Resolved, req *httpclient.Request, handles map[string]*script.Handle) {
	if len(handles) == 0 {
		return
	}
	for i := range req.Headers {
		h, ok := handles[script.HeaderSlot(req.Headers[i].Name)]
		if !ok {
			continue
		}
		req.Headers[i].Value = h.Reveal()
		// The composed text is what was substituted, so masking that catches
		// it wherever it turns up. The bare value exists nowhere else.
		res.AddSecret(h.Reveal())
	}
	if h, ok := handles[script.BodySlot]; ok {
		req.Body = []byte(h.Reveal())
		res.AddSecret(h.Reveal())
	}
}

// ResponseHeaders converts response headers for a script.
func ResponseHeaders(headers http.Header) []script.HeaderPair {
	var out []script.HeaderPair
	for name, values := range headers {
		for _, value := range values {
			out = append(out, script.HeaderPair{Name: name, Value: value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StatusText is the reason phrase out of "201 Created".
func StatusText(status string) string {
	if _, text, ok := strings.Cut(status, " "); ok {
		return text
	}
	return status
}
