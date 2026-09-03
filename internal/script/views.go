package script

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

// RequestView is the request a script can read and shape (docs/FORMAT.md §9.5).
//
// In the Pre phase every value is the **template**: `{{references}}` are still
// unresolved, because resolution happens after the scripts run (§9.2). That is
// what lets a folder header reference a value the script is about to set.
type RequestView struct {
	Method string
	URL    string
	Body   string
	// Headers are the effective headers in send order.
	Headers []HeaderPair
	// Path and Name identify the request, read-only.
	Path string
	Name string

	// changed reports that a script altered something, so the sender knows to
	// take these values rather than the file's.
	changed bool
	// handles are the secret handles a script placed, keyed by slot:
	// "header:Authorization" or "body".
	handles map[string]*Handle
}

// HeaderPair is one header, in order.
type HeaderPair struct {
	Name  string
	Value string
}

// Changed reports whether a script altered the request.
func (v *RequestView) Changed() bool { return v.changed }

// Handles are the secret handles a script placed on the request.
func (v *RequestView) Handles() map[string]*Handle { return v.handles }

// HeaderSlot is the handles key for a header.
func HeaderSlot(name string) string { return "header:" + strings.ToLower(name) }

// BodySlot is the handles key for the body.
const BodySlot = "body"

// ResponseView is what came back (docs/FORMAT.md §9.6).
type ResponseView struct {
	Status     int
	StatusText string
	Headers    []HeaderPair
	Body       string
	Size       int64
	Timings    Timings
}

// Timings are the milliseconds a send took, by phase.
type Timings struct {
	DNS     float64
	Connect float64
	TLS     float64
	TTFB    float64
	Total   float64
}

// requestAPI installs `request`.
func (r *Runtime) requestAPI() (*goja.Object, error) {
	view := r.opts.Request
	if view.handles == nil {
		view.handles = map[string]*Handle{}
	}
	obj := r.vm.NewObject()

	// Method, URL and body are accessors so a plain assignment works and is
	// recorded: `request.url = ...` has to mark the request changed.
	mutable := r.opts.Phase == Pre
	define := func(name string, get func() goja.Value, set func(goja.Value)) error {
		return obj.DefineAccessorProperty(name,
			r.vm.ToValue(func(goja.FunctionCall) goja.Value { return get() }),
			r.vm.ToValue(func(call goja.FunctionCall) goja.Value {
				if !mutable {
					panic(r.vm.NewTypeError(
						"request.%s cannot be changed in a post-response script: the request has already been sent", name))
				}
				if len(call.Arguments) > 0 {
					set(call.Argument(0))
				}
				return goja.Undefined()
			}),
			goja.FLAG_FALSE, goja.FLAG_TRUE)
	}

	if err := define("method",
		func() goja.Value { return r.vm.ToValue(view.Method) },
		func(v goja.Value) {
			text, secret := r.text(v)
			if secret {
				panic(r.vm.NewTypeError("request.method cannot be a secret"))
			}
			view.Method, view.changed = strings.ToUpper(strings.TrimSpace(text)), true
		}); err != nil {
		return nil, err
	}
	if err := define("url",
		func() goja.Value { return r.vm.ToValue(view.URL) },
		func(v goja.Value) {
			text, secret := r.text(v)
			if secret {
				// A URL is recorded, logged by the server and often in a
				// proxy's history. A secret in one is a secret that has left.
				panic(r.vm.NewTypeError(
					"request.url cannot be a secret: a URL is recorded by every hop it passes"))
			}
			view.URL, view.changed = text, true
		}); err != nil {
		return nil, err
	}
	if err := define("body",
		func() goja.Value { return r.vm.ToValue(view.Body) },
		func(v goja.Value) {
			if h := r.handleOf(v); h != nil {
				view.handles[BodySlot] = h
				view.Body, view.changed = h.masked(), true
				return
			}
			text, _ := r.text(v)
			delete(view.handles, BodySlot)
			view.Body, view.changed = text, true
		}); err != nil {
		return nil, err
	}

	headers, err := r.headersAPI(view, mutable)
	if err != nil {
		return nil, err
	}
	if err := obj.Set("headers", headers); err != nil {
		return nil, err
	}
	if err := obj.Set("path", view.Path); err != nil {
		return nil, err
	}
	if err := obj.Set("name", view.Name); err != nil {
		return nil, err
	}
	return obj, nil
}

// headersAPI installs `request.headers`.
func (r *Runtime) headersAPI(view *RequestView, mutable bool) (*goja.Object, error) {
	obj := r.vm.NewObject()
	refuse := func(what string) {
		if !mutable {
			panic(r.vm.NewTypeError(
				"request.headers.%s cannot be used in a post-response script: the request has already been sent", what))
		}
	}

	if err := obj.Set("get", func(name string) goja.Value {
		for _, h := range view.Headers {
			if strings.EqualFold(h.Name, name) {
				return r.vm.ToValue(h.Value)
			}
		}
		return goja.Undefined()
	}); err != nil {
		return nil, err
	}

	if err := obj.Set("names", func() []string {
		out := make([]string, 0, len(view.Headers))
		for _, h := range view.Headers {
			out = append(out, h.Name)
		}
		return out
	}); err != nil {
		return nil, err
	}

	// set replaces every header of that name; a handle is remembered by slot
	// so the sender can put the real value in at prepare time.
	if err := obj.Set("set", func(name string, value goja.Value) {
		refuse("set")
		text := r.headerText(view, name, value)
		kept := view.Headers[:0:0]
		replaced := false
		for _, h := range view.Headers {
			if !strings.EqualFold(h.Name, name) {
				kept = append(kept, h)
				continue
			}
			if !replaced {
				kept = append(kept, HeaderPair{Name: name, Value: text})
				replaced = true
			}
		}
		if !replaced {
			kept = append(kept, HeaderPair{Name: name, Value: text})
		}
		view.Headers, view.changed = kept, true
	}); err != nil {
		return nil, err
	}

	if err := obj.Set("add", func(name string, value goja.Value) {
		refuse("add")
		text := r.headerText(view, name, value)
		view.Headers = append(view.Headers, HeaderPair{Name: name, Value: text})
		view.changed = true
	}); err != nil {
		return nil, err
	}

	if err := obj.Set("remove", func(name string) {
		refuse("remove")
		kept := view.Headers[:0:0]
		for _, h := range view.Headers {
			if !strings.EqualFold(h.Name, name) {
				kept = append(kept, h)
			}
		}
		delete(view.handles, HeaderSlot(name))
		view.Headers, view.changed = kept, true
	}); err != nil {
		return nil, err
	}
	return obj, nil
}

// headerText converts a header value, remembering a handle by slot.
//
// The stored text is the *mask*, so everything that reads the request back —
// the response pane's "what was sent", a log line, an error — sees the mask.
// The real value is substituted once, by the sender, from the handle.
//
// undefined and null are refused rather than sent as "undefined": a header
// reading that on the wire is a worse outcome than a message naming the call.
func (r *Runtime) headerText(view *RequestView, name string, value goja.Value) string {
	slot := HeaderSlot(name)
	if h := r.handleOf(value); h != nil {
		view.handles[slot] = h
		return h.masked()
	}
	r.refuseEmpty("request.headers", name, value)
	delete(view.handles, slot)
	text, _ := r.text(value)
	return text
}

// refuseEmpty rejects undefined and null, which are almost always a bug at a
// setter rather than an intention.
func (r *Runtime) refuseEmpty(what, name string, value goja.Value) {
	switch {
	case value == nil || goja.IsUndefined(value):
		panic(r.vm.NewTypeError("%s.set(%q): the value is undefined", what, name))
	case goja.IsNull(value):
		panic(r.vm.NewTypeError("%s.set(%q): the value is null", what, name))
	}
}

// responseAPI installs `response`.
func (r *Runtime) responseAPI() (*goja.Object, error) {
	view := r.opts.Response
	obj := r.vm.NewObject()

	fields := map[string]any{
		"status":     view.Status,
		"statusText": view.StatusText,
		"ok":         view.Status > 0 && view.Status < 400,
		"body":       view.Body,
		"size":       view.Size,
	}
	for _, name := range sortedKeys(fields) {
		if err := obj.Set(name, fields[name]); err != nil {
			return nil, err
		}
	}

	timings := r.vm.NewObject()
	for name, value := range map[string]float64{
		"dns": view.Timings.DNS, "connect": view.Timings.Connect,
		"tls": view.Timings.TLS, "ttfb": view.Timings.TTFB, "total": view.Timings.Total,
	} {
		if err := timings.Set(name, value); err != nil {
			return nil, err
		}
	}
	if err := obj.Set("timings", timings); err != nil {
		return nil, err
	}

	headers := r.vm.NewObject()
	if err := headers.Set("get", func(name string) goja.Value {
		for _, h := range view.Headers {
			if strings.EqualFold(h.Name, name) {
				return r.vm.ToValue(h.Value)
			}
		}
		return goja.Undefined()
	}); err != nil {
		return nil, err
	}
	if err := headers.Set("names", func() []string {
		out := make([]string, 0, len(view.Headers))
		for _, h := range view.Headers {
			out = append(out, h.Name)
		}
		return out
	}); err != nil {
		return nil, err
	}
	if err := obj.Set("headers", headers); err != nil {
		return nil, err
	}

	// json() parses on demand and throws the parse error, which is more use
	// than a null: a script asserting on a body that is not JSON wants to
	// know that is what happened.
	if err := obj.Set("json", func() goja.Value {
		var parsed any
		if err := json.Unmarshal([]byte(view.Body), &parsed); err != nil {
			panic(r.vm.NewTypeError("response.json(): the body is not JSON: %s", err.Error()))
		}
		return r.vm.ToValue(parsed)
	}); err != nil {
		return nil, err
	}
	return obj, nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
