// Package postman imports Postman Collection v2.1 exports (and the v2.0 auth
// shape) into an Otis collection on disk. See docs/FORMAT.md §7 for the
// output conventions.
package postman

import (
	"encoding/json"
	"strings"
)

// collection is the subset of the Postman schema the importer reads. Fields
// that Postman serialises polymorphically are json.RawMessage and decoded by
// the helpers below.
type pmCollection struct {
	Info struct {
		Name        string          `json:"name"`
		Description json.RawMessage `json:"description"`
		Schema      string          `json:"schema"`
	} `json:"info"`
	Item     []item          `json:"item"`
	Auth     json.RawMessage `json:"auth"`
	Event    []event         `json:"event"`
	Variable []variable      `json:"variable"`
}

type item struct {
	Name        string          `json:"name"`
	Description json.RawMessage `json:"description"`
	// Item is present (possibly empty) for folders and absent for requests.
	Item     *[]item         `json:"item"`
	Request  json.RawMessage `json:"request"` // object, or a string URL
	Event    []event         `json:"event"`
	Auth     json.RawMessage `json:"auth"`
	Variable []variable      `json:"variable"`
}

func (it item) isFolder() bool { return it.Item != nil }

type request struct {
	Method      string          `json:"method"`
	Header      json.RawMessage `json:"header"` // []header, or a string of "Key: value" lines
	Body        *body           `json:"body"`
	URL         json.RawMessage `json:"url"` // string, or urlObject
	Auth        json.RawMessage `json:"auth"`
	Description json.RawMessage `json:"description"`
}

type header struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type urlObject struct {
	Raw      string          `json:"raw"`
	Protocol string          `json:"protocol"`
	Host     json.RawMessage `json:"host"` // []string or string
	Port     string          `json:"port"`
	Path     json.RawMessage `json:"path"` // []string (elements may be objects) or string
	Query    []queryParam    `json:"query"`
	Hash     string          `json:"hash"`
	Variable []variable      `json:"variable"`
}

type queryParam struct {
	Key      string  `json:"key"`
	Value    *string `json:"value"`
	Disabled bool    `json:"disabled"`
}

type body struct {
	Mode       string      `json:"mode"`
	Raw        string      `json:"raw"`
	Urlencoded []formParam `json:"urlencoded"`
	Formdata   []formParam `json:"formdata"`
	File       *struct {
		Src json.RawMessage `json:"src"`
	} `json:"file"`
	Graphql *struct {
		Query     string `json:"query"`
		Variables string `json:"variables"`
	} `json:"graphql"`
	Options *struct {
		Raw *struct {
			Language string `json:"language"`
		} `json:"raw"`
	} `json:"options"`
	Disabled bool `json:"disabled"`
}

type formParam struct {
	Key      string          `json:"key"`
	Value    string          `json:"value"`
	Type     string          `json:"type"` // "text" or "file"
	Src      json.RawMessage `json:"src"`  // string or []string
	Disabled bool            `json:"disabled"`
}

type event struct {
	Listen string `json:"listen"` // "prerequest" or "test"
	Script *struct {
		Exec json.RawMessage `json:"exec"` // []string or string
	} `json:"script"`
	Disabled bool `json:"disabled"`
}

type variable struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Type     string          `json:"type"` // "string", "secret", ...
	Disabled bool            `json:"disabled"`
	Enabled  *bool           `json:"enabled"` // environment exports use enabled instead of disabled
}

func (v variable) active() bool {
	if v.Enabled != nil {
		return *v.Enabled
	}
	return !v.Disabled
}

// environment is a Postman environment export.
type environment struct {
	Name   string     `json:"name"`
	Values []variable `json:"values"`
}

// auth is decoded from either the v2.1 shape ({"type":"bearer","bearer":
// [{"key":"token","value":"x"}]}) or the v2.0 shape ({"type":"bearer",
// "bearer":{"token":"x"}}). Params holds the type-specific key/value pairs.
type auth struct {
	Type   string
	Params map[string]string
}

func decodeAuth(raw json.RawMessage) (*auth, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	a := &auth{Params: map[string]string{}}
	if err := json.Unmarshal(m["type"], &a.Type); err != nil {
		return nil, err
	}
	params, ok := m[a.Type]
	if !ok || len(params) == 0 {
		return a, nil
	}
	// v2.1: array of {key, value}
	var list []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(params, &list); err == nil {
		for _, kv := range list {
			a.Params[kv.Key] = rawToString(kv.Value)
		}
		return a, nil
	}
	// v2.0: object of key -> value
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(params, &obj); err != nil {
		return nil, err
	}
	for k, v := range obj {
		a.Params[k] = rawToString(v)
	}
	return a, nil
}

// descriptionText handles "description" as a string or {"content": ...}.
func descriptionText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Content
	}
	return ""
}

// rawToString renders a scalar JSON value as text: strings verbatim,
// numbers and booleans as written, anything else as compact JSON.
func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// stringList decodes a JSON string or array of strings (array elements that
// are objects contribute their "value" field, which Postman uses for path
// segments with metadata).
func stringList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, el := range list {
		var es string
		if json.Unmarshal(el, &es) == nil {
			out = append(out, es)
			continue
		}
		var obj struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(el, &obj) == nil {
			out = append(out, obj.Value)
		}
	}
	return out
}

func decodeHeaders(raw json.RawMessage) []header {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []header
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	var out []header
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			out = append(out, header{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
		}
	}
	return out
}

func decodeRequest(raw json.RawMessage) (*request, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		u, _ := json.Marshal(s)
		return &request{Method: "GET", URL: u}, nil
	}
	var r request
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	return &r, nil
}
