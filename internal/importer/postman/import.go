package postman

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
)

// Output is a planned import: every file to write, keyed by path relative
// to the output directory, plus the report. Nothing has touched disk yet.
type Output struct {
	Files  map[string]string
	Report *Report
}

// Plan converts a Postman collection export (and optional environment
// exports) into the files of an Otis collection, without writing anything.
func Plan(collectionJSON []byte, environmentJSONs ...[]byte) (*Output, error) {
	var col pmCollection
	if err := json.Unmarshal(collectionJSON, &col); err != nil {
		return nil, fmt.Errorf("collection: invalid JSON: %w", err)
	}
	if col.Info.Name == "" && len(col.Item) == 0 {
		return nil, fmt.Errorf("collection: not a Postman collection export (no info.name and no items)")
	}
	if col.Info.Schema != "" && !strings.Contains(col.Info.Schema, "v2.") {
		return nil, fmt.Errorf("collection: unsupported schema %q; export as Collection v2.1", col.Info.Schema)
	}
	p := &planner{
		out:    &Output{Files: map[string]string{}, Report: &Report{CollectionName: col.Info.Name}},
		usedID: map[string]bool{},
	}
	p.folder("", col.Info.Name, descriptionText(col.Info.Description), col.Auth, col.Variable, col.Event, col.Item)
	for _, envJSON := range environmentJSONs {
		if err := p.environment(envJSON); err != nil {
			return nil, err
		}
	}
	// Postman dynamic variables without an Otis equivalent, once per file.
	for _, rel := range sortedKeys(p.out.Files) {
		seen := map[string]bool{}
		for _, m := range unsupportedDynamicRe.FindAllString(p.out.Files[rel], -1) {
			if !seen[m] && !supportedBuiltin(m) {
				seen[m] = true
				p.rep().warn(rel, "Postman dynamic variable %s has no Otis equivalent and will be reported as unresolved", m)
			}
		}
	}
	return p.out, nil
}

func supportedBuiltin(ref string) bool {
	for _, to := range translations {
		if ref == to {
			return true
		}
	}
	return false
}

type planner struct {
	out    *Output
	usedID map[string]bool
}

func (p *planner) rep() *Report { return p.out.Report }

// layout fixes the order of preamble items in generated files: @name, then
// the description and any TODO comments, then other directives such as
// @auth, then variables. Line numbers are what the serializer sorts on.
func layout(r *httpfile.Request) {
	line := 0
	next := func() int { line++; return line }
	for i := range r.Directives {
		if r.Directives[i].Name == "name" {
			r.Directives[i].Line = next()
		}
	}
	for i := range r.Comments {
		r.Comments[i].Line = next()
	}
	for i := range r.Directives {
		if r.Directives[i].Name != "name" {
			r.Directives[i].Line = next()
		}
	}
	for i := range r.Variables {
		r.Variables[i].Line = next()
	}
}

func (p *planner) writeEntry(rel string, r *httpfile.Request) {
	layout(r)
	p.write(rel, (&httpfile.File{Requests: []*httpfile.Request{r}}).String())
}

func (p *planner) write(rel, content string) {
	p.out.Files[rel] = content
	p.rep().Files = append(p.rep().Files, rel)
}

// folder emits a folder's _folder.http, scripts, .order and children.
// dir is "" for the collection root.
func (p *planner) folder(dir, name, description string, authRaw json.RawMessage, vars []variable, events []event, items []item) {
	settings := &httpfile.Request{}
	if description != "" {
		settings.Comments = commentLines(description)
	}
	for _, v := range vars {
		if !v.active() {
			p.rep().skip(path.Join(dir, collection.FolderFileName), "disabled variable %q", v.Key)
			continue
		}
		if v.Type == "secret" {
			p.rep().skip(path.Join(dir, collection.FolderFileName), "secret variable %q: move it to an environment as {\"$secret\": \"keychain\"}", v.Key)
			continue
		}
		settings.Variables = append(settings.Variables, httpfile.Variable{Name: v.Key, Value: translate(rawToString(v.Value))})
	}
	p.applyAuth(settings, authRaw, path.Join(dir, collection.FolderFileName), true)
	if dir != "" && len(items) == 0 && len(settings.Comments)+len(settings.Variables)+len(settings.Directives)+len(settings.Headers) == 0 {
		// A directory with no files would not survive git; give it a
		// folder file so the folder (and its .order entry) stay valid.
		settings.Comments = []httpfile.Comment{{Style: "#", Text: " Imported from Postman; this folder was empty."}}
	}
	if len(settings.Comments)+len(settings.Variables)+len(settings.Directives)+len(settings.Headers) > 0 {
		p.writeEntry(path.Join(dir, collection.FolderFileName), settings)
	}
	p.scripts(dir, "", name, events)

	var order []string
	used := map[string]bool{}
	for _, it := range items {
		if it.isFolder() {
			slug := collection.UniqueName(used, collection.Slug(it.Name), "folder")
			order = append(order, slug+"/")
			p.rep().Folders++
			p.folder(path.Join(dir, slug), it.Name, descriptionText(it.Description), it.Auth, it.Variable, it.Event, *it.Item)
			continue
		}
		slug := collection.UniqueName(used, collection.Slug(it.Name), "request")
		order = append(order, slug+collection.RequestExt)
		p.request(dir, slug, it)
	}
	if len(order) > 0 {
		p.write(path.Join(dir, collection.OrderFileName), strings.Join(order, "\n")+"\n")
	}
}

// request emits one <slug>.http and its sidecar scripts.
func (p *planner) request(dir, slug string, it item) {
	rel := path.Join(dir, slug+collection.RequestExt)
	p.rep().Requests++
	req, err := decodeRequest(it.Request)
	if err != nil || req == nil {
		p.rep().skip(rel, "request %q could not be decoded: %v", it.Name, err)
		return
	}
	r := &httpfile.Request{Method: strings.ToUpper(req.Method)}
	if desc := descriptionText(it.Description); desc != "" {
		r.Comments = commentLines(desc)
	} else if desc := descriptionText(req.Description); desc != "" {
		r.Comments = commentLines(desc)
	}
	if name := strings.TrimSpace(it.Name); name != "" {
		r.Directives = append(r.Directives, httpfile.Directive{Style: "#", Name: "name", Value: name})
	} else {
		p.rep().note(rel, "request had no name in Postman; named after its file")
	}

	r.URL = p.buildURL(rel, req.URL, r)
	for _, h := range decodeHeaders(req.Header) {
		if h.Disabled {
			p.rep().skip(rel, "disabled header %q", h.Key)
			continue
		}
		if strings.TrimSpace(h.Key) == "" {
			continue
		}
		r.Headers = append(r.Headers, httpfile.Header{Name: h.Key, Value: translate(h.Value)})
	}
	p.applyAuth(r, req.Auth, rel, false)
	p.body(rel, req.Body, r)

	p.writeEntry(rel, r)
	p.scripts(dir, slug, it.Name, it.Event)
}

// buildURL renders a Postman URL (string or object). Path variables (":id")
// become {{id}} with an @id declaration when Postman carried a value.
func (p *planner) buildURL(rel string, raw json.RawMessage, r *httpfile.Request) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return translate(s)
	}
	var u urlObject
	if err := json.Unmarshal(raw, &u); err != nil {
		p.rep().warn(rel, "URL could not be decoded (%v); left empty", err)
		return ""
	}
	host := stringList(u.Host)
	pathSegs := stringList(u.Path)
	var out string
	if len(host) == 0 && u.Raw != "" {
		// Nothing structured to rebuild from: trust raw.
		out = u.Raw
	} else {
		var b strings.Builder
		if u.Protocol != "" {
			b.WriteString(u.Protocol + "://")
		}
		b.WriteString(strings.Join(host, "."))
		if u.Port != "" {
			b.WriteString(":" + u.Port)
		}
		if len(pathSegs) > 0 {
			b.WriteString("/" + strings.Join(pathSegs, "/"))
		}
		var q []string
		for _, qp := range u.Query {
			if qp.Disabled {
				p.rep().skip(rel, "disabled query parameter %q", qp.Key)
				continue
			}
			if qp.Value == nil {
				q = append(q, qp.Key)
			} else {
				q = append(q, qp.Key+"="+*qp.Value)
			}
		}
		if len(q) > 0 {
			b.WriteString("?" + strings.Join(q, "&"))
		}
		if u.Hash != "" {
			b.WriteString("#" + u.Hash)
		}
		out = b.String()
	}
	// Postman path variables.
	for _, v := range u.Variable {
		if v.Key == "" {
			continue
		}
		out = strings.ReplaceAll(out, ":"+v.Key, "{{"+v.Key+"}}")
		if val := rawToString(v.Value); val != "" {
			r.Variables = append(r.Variables, httpfile.Variable{Name: v.Key, Value: translate(val)})
		} else {
			p.rep().warn(rel, "path variable %q has no value; define {{%s}} in an environment", v.Key, v.Key)
		}
	}
	return translate(out)
}

// applyAuth maps a Postman auth block onto a request or folder entry.
func (p *planner) applyAuth(r *httpfile.Request, raw json.RawMessage, rel string, isFolder bool) {
	a, err := decodeAuth(raw)
	if err != nil {
		p.rep().skip(rel, "auth could not be decoded: %v", err)
		return
	}
	if a == nil || a.Type == "" || a.Type == "inherit" {
		return
	}
	directive := func(value string) {
		r.Directives = append(r.Directives, httpfile.Directive{Style: "#", Name: resolve.AuthDirective, Value: value})
	}
	get := func(k string) string { return translate(a.Params[k]) }
	switch a.Type {
	case "noauth":
		directive("none")
	case "bearer":
		directive("bearer " + get("token"))
		p.literalCredential(rel, "bearer token", a.Params["token"])
	case "basic":
		v := "basic " + get("username")
		if pw := get("password"); pw != "" {
			v += " " + pw
		}
		directive(v)
		p.literalCredential(rel, "basic password", a.Params["password"])
	case "apikey":
		key, value := get("key"), get("value")
		switch a.Params["in"] {
		case "query":
			if isFolder {
				p.rep().skip(rel, "API key in query string cannot be inherited by a folder; add ?%s=... to each request", key)
				return
			}
			sep := "?"
			if strings.Contains(r.URL, "?") {
				sep = "&"
			}
			r.URL += sep + key + "=" + value
			p.rep().note(rel, "API key appended to the URL as %s=...", key)
		default:
			r.Headers = append(r.Headers, httpfile.Header{Name: key, Value: value})
		}
		p.literalCredential(rel, "API key", a.Params["value"])
	case "awsv4":
		var parts []string
		accessKey, secretKey, token := get("accessKey"), get("secretKey"), get("sessionToken")
		if accessKey == "" && secretKey == "" {
			// Nothing configured in Postman: fall back to the local profile chain.
			p.rep().note(rel, "AWS auth had no keys in Postman; using the local AWS credential chain (@auth aws)")
		} else {
			parts = append(parts, "key="+orVar(accessKey, "awsAccessKey"))
			if !isVarRef(secretKey) {
				p.rep().warn(rel, "AWS secret key was a literal in the export; replaced with {{awsSecretKey}}. Set it as a keychain secret, or switch to '@auth aws profile=<name>' to use your local AWS credentials")
				secretKey = "{{awsSecretKey}}"
			}
			parts = append(parts, "secret="+secretKey)
			if token != "" {
				if !isVarRef(token) {
					p.rep().warn(rel, "AWS session token was a literal in the export; replaced with {{awsSessionToken}}")
					token = "{{awsSessionToken}}"
				}
				parts = append(parts, "token="+token)
			}
			p.rep().note(rel, "AWS auth uses explicit keys; consider '@auth aws profile=<name>' so no key material lives in the collection")
		}
		if region := get("region"); region != "" {
			parts = append(parts, "region="+region)
		}
		if service := get("service"); service != "" {
			parts = append(parts, "service="+service)
		}
		directive(strings.TrimSpace("aws " + strings.Join(parts, " ")))
	default:
		p.rep().skip(rel, "auth type %q is not supported; configure it manually", a.Type)
		r.Comments = append(r.Comments, httpfile.Comment{Style: "#", Text: fmt.Sprintf(" TODO: Postman auth type %q is not supported by Otis; configure auth manually.", a.Type)})
	}
}

// literalCredential warns when a credential is a literal rather than a
// {{variable}} reference.
func (p *planner) literalCredential(rel, what, value string) {
	if value != "" && !isVarRef(value) {
		p.rep().warn(rel, "%s is a literal value in the collection; move it to an environment variable", what)
	}
}

func isVarRef(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}")
}

func orVar(s, fallback string) string {
	if s == "" {
		return "{{" + fallback + "}}"
	}
	return s
}

// body renders the request body according to its Postman mode.
func (p *planner) body(rel string, b *body, r *httpfile.Request) {
	if b == nil || b.Disabled || b.Mode == "" || b.Mode == "none" {
		if b != nil && b.Disabled {
			p.rep().skip(rel, "body is disabled")
		}
		return
	}
	setContentType := func(ct string) {
		if _, ok := r.Header("Content-Type"); !ok {
			r.Headers = append(r.Headers, httpfile.Header{Name: "Content-Type", Value: ct})
			p.rep().note(rel, "added Content-Type: %s (Postman sets it implicitly)", strings.SplitN(ct, ";", 2)[0])
		}
	}
	switch b.Mode {
	case "raw":
		r.Body.Raw = translate(strings.ReplaceAll(b.Raw, "\r\n", "\n"))
		if b.Options != nil && b.Options.Raw != nil {
			if ct, ok := rawLanguageContentTypes[b.Options.Raw.Language]; ok {
				setContentType(ct)
			}
		}
	case "urlencoded":
		var pairs []string
		for _, f := range b.Urlencoded {
			if f.Disabled {
				p.rep().skip(rel, "disabled form field %q", f.Key)
				continue
			}
			pairs = append(pairs, formEscape(f.Key)+"="+formEscape(translate(f.Value)))
		}
		r.Body.Raw = strings.Join(pairs, "&")
		setContentType("application/x-www-form-urlencoded")
	case "formdata":
		const boundary = "----OtisFormBoundary"
		var parts []string
		for _, f := range b.Formdata {
			if f.Disabled {
				p.rep().skip(rel, "disabled form field %q", f.Key)
				continue
			}
			if f.Type == "file" {
				for _, src := range stringList(f.Src) {
					parts = append(parts, fmt.Sprintf("--%s\nContent-Disposition: form-data; name=%q; filename=%q\n\n< %s", boundary, f.Key, path.Base(src), src))
					p.rep().warn(rel, "form file %q points at %s, a path on the exporting machine; copy the file into the collection and fix the path", f.Key, src)
				}
				continue
			}
			parts = append(parts, fmt.Sprintf("--%s\nContent-Disposition: form-data; name=%q\n\n%s", boundary, f.Key, translate(f.Value)))
		}
		r.Body.Raw = strings.Join(parts, "\n") + "\n--" + boundary + "--"
		setContentType("multipart/form-data; boundary=" + boundary)
	case "file":
		if b.File == nil {
			return
		}
		srcs := stringList(b.File.Src)
		if len(srcs) == 0 {
			p.rep().skip(rel, "file body has no source")
			return
		}
		r.Body.FilePath = srcs[0]
		p.rep().warn(rel, "body file %s is a path on the exporting machine; copy the file into the collection and fix the path", srcs[0])
	case "graphql":
		if b.Graphql == nil {
			return
		}
		envelope := map[string]json.RawMessage{"query": mustJSON(translate(b.Graphql.Query))}
		if vars := strings.TrimSpace(b.Graphql.Variables); vars != "" {
			if json.Valid([]byte(vars)) {
				envelope["variables"] = json.RawMessage(vars)
			} else {
				envelope["variables"] = mustJSON(translate(vars))
				p.rep().warn(rel, "GraphQL variables were not valid JSON; kept as a string")
			}
		}
		enc, _ := json.MarshalIndent(envelope, "", "  ")
		r.Body.Raw = translate(string(enc))
		setContentType("application/json")
	default:
		p.rep().skip(rel, "body mode %q is not supported", b.Mode)
	}
}

var rawLanguageContentTypes = map[string]string{
	"json": "application/json",
	"xml":  "application/xml",
	"html": "text/html",
	"text": "text/plain",
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// formEscape percent-encodes a form value while leaving {{references}}
// intact so they still resolve.
func formEscape(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(url.QueryEscape(s))
			break
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			b.WriteString(url.QueryEscape(s))
			break
		}
		b.WriteString(url.QueryEscape(s[:start]))
		b.WriteString(s[start : start+end+2])
		s = s[start+end+2:]
	}
	return b.String()
}

// scripts writes each Postman script next to its owner, translated as far as
// it safely can be (translate.go).
//
// **The name it gets is the whole decision, and it turns on one thing: does
// anything untranslatable survive?**
//
//   - Nothing does — the file becomes a *hook*: `_pre.js` / `_post.js` for a
//     folder, `<slug>.pre.js` / `<slug>.post.js` beside a request. Otis runs
//     it, which is the point of importing a script at all.
//   - Something does — the file becomes a *module*: `_postman-pre.js`,
//     `<slug>.postman-post.js`. Nothing runs it until somebody finishes the
//     port, and the header names the line that stopped it.
//
// The second branch is not caution for its own sake. An earlier version of
// this function wrote hook names for *every* imported script while stamping
// "NOT executed" at the top of each, so all of them ran on the first send and
// threw at the first `pm.` they reached — six of them in one real collection,
// on every request, with the header insisting they were inert. The
// completeness check is what makes the hook branch safe, and it is why
// Translate refuses to make a substitution whose result is merely plausible.
func (p *planner) scripts(dir, slug, ownerName string, events []event) {
	for _, ev := range events {
		if ev.Script == nil {
			continue
		}
		var kind, file string
		switch ev.Listen {
		case "prerequest":
			kind, file = "pre-request", "pre"
		case "test":
			kind, file = "test", "post"
		default:
			continue
		}
		code := strings.TrimRight(strings.Join(stringList(ev.Script.Exec), "\n"), "\n")
		if strings.TrimSpace(code) == "" {
			continue
		}
		if ev.Disabled {
			p.rep().skip(path.Join(dir, moduleName(slug, file)),
				"disabled %s script for %q", kind, ownerName)
			continue
		}

		out := Translate(code)
		if out.Complete {
			rel := path.Join(dir, hookName(slug, file))
			p.write(rel, fmt.Sprintf(translatedHeader,
				kind, ownerName, strings.Join(out.summary(), "\n"), out.Code))
			p.rep().note(rel, "%s script for %q translated and will run", kind, ownerName)
			continue
		}

		rel := path.Join(dir, moduleName(slug, file))
		p.write(rel, fmt.Sprintf(untranslatedHeader,
			kind, ownerName, blockerLines(out), hookName(slug, file), out.Code))
		p.rep().warn(rel, "%s script for %q needs %s; imported as a module, so nothing runs it until you port it to %s",
			kind, ownerName, out.Blockers[0].What, hookName(slug, file))
	}
}

// moduleName is the name a script gets when it could not be finished: one
// docs/FORMAT.md §2.4 makes a plain module, so nothing runs it.
func moduleName(slug, phase string) string {
	if slug == "" {
		return "_postman-" + phase + ".js"
	}
	return slug + ".postman-" + phase + ".js"
}

// blockerLines is what the header says about why the file is not a hook.
func blockerLines(t Translation) string {
	var b strings.Builder
	for _, blocker := range t.Blockers {
		fmt.Fprintf(&b, "//   line %d, %s — %s\n", blocker.Line, blocker.What, blocker.Why)
	}
	return strings.TrimRight(b.String(), "\n")
}

// untranslatedHeader is what sits above an imported Postman script.
//
// It names the file the ported version belongs in, because that is the step
// somebody has to take and the old header did not say where to take it — and
// it maps the three Postman calls that account for almost every script anyone
// has written. `setEnvironmentVariable` is the one that matters: it is how a
// Postman collection chains a flow, and `vars.session.set` is Otis' answer
// (docs/FORMAT.md §4.5), so a collection that chained in Postman chains here
// once these lines are translated.
const untranslatedHeader = `// Postman %s script for %q, translated as far as it goes.
//
// **Nothing runs this file.** It is a plain ES module (docs/FORMAT.md §2.4),
// because something in it has no Otis equivalent:
//
%s
//
// The rest was translated. Finish the port and rename the file to %s to make
// Otis run it, or move what you need into a {%% %%} block in the request.
//
// A script gets a JavaScript realm and nothing else: no fetch, no require, no
// filesystem, no timers (docs/FORMAT.md §9.3).

%s
`

// translatedHeader sits above a script that came through whole.
//
// It says what changed rather than leaving the reader to diff against a
// Postman export they may not have, and it names the one substitution that
// was a *decision*: Postman's variable setters all write somewhere that
// persists, and this writes a session value, which is written to no file at
// all (docs/FORMAT.md §4.5). `vars.env.set` is the other call, and it changes
// a committed file — which is why an importer does not reach for it.
const translatedHeader = `// Postman %s script for %q, translated.
//
// **This runs.** It is a hook (docs/FORMAT.md §2.4), so Otis executes it
// around the request it belongs to, exactly as Postman did.
//
// What was substituted:
//
%s
//
// A Postman variable set became vars.session.set: in memory, on this machine,
// written to no file, and visible to every request in this folder and below
// (docs/FORMAT.md §4.5). If you meant the value to persist, vars.env.set
// writes the active environment file — and shows up in git.

%s
`

// hookName is the file an imported script would have to become in order to
// run: the hook names of docs/FORMAT.md §2.4.
func hookName(slug, phase string) string {
	if slug == "" {
		return "_" + phase + ".js"
	}
	return slug + "." + phase + ".js"
}

// environment writes env/<slug>.json from a Postman environment export.
func (p *planner) environment(data []byte) error {
	var env environment
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("environment: invalid JSON: %w", err)
	}
	if env.Name == "" || env.Values == nil {
		return fmt.Errorf("environment: not a Postman environment export (missing name or values)")
	}
	rel := resolve.EnvPath(collection.Slug(env.Name))
	p.rep().Environments++
	var b strings.Builder
	b.WriteString("{\n")
	var secretNames []string
	first := true
	for _, v := range env.Values {
		if !v.active() {
			p.rep().skip(rel, "disabled variable %q", v.Key)
			continue
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		key, _ := json.Marshal(v.Key)
		if v.Type == "secret" {
			secretNames = append(secretNames, v.Key)
			fmt.Fprintf(&b, "  %s: {\"$secret\": \"keychain\"}", key)
			continue
		}
		val, _ := json.Marshal(translate(rawToString(v.Value)))
		fmt.Fprintf(&b, "  %s: %s", key, val)
	}
	if !first {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	p.write(rel, b.String())
	if len(secretNames) > 0 {
		p.rep().warn(rel, "secret variables written as keychain references; their values were NOT imported and must be stored in the keychain: %s", strings.Join(secretNames, ", "))
	}
	return nil
}

// translate maps Postman dynamic variables onto Otis builtins where an
// equivalent exists. Others are left as written.
var translations = map[string]string{
	"{{$guid}}":         "{{$uuid}}",
	"{{$randomUUID}}":   "{{$uuid}}",
	"{{$timestamp}}":    "{{$timestamp}}",
	"{{$isoTimestamp}}": "{{$isoTimestamp}}",
	"{{$randomInt}}":    "{{$randomInt}}",
}

func translate(s string) string {
	if !strings.Contains(s, "{{$") {
		return s
	}
	for from, to := range translations {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

// unsupportedDynamicRe finds Postman dynamic variables Otis does not have.
var unsupportedDynamicRe = regexp.MustCompile(`\{\{\$[A-Za-z0-9_.()\-, ]+\}\}`)

// commentLines wraps a description into "#" comment lines at 78 columns,
// preserving paragraph breaks.
func commentLines(text string) []httpfile.Comment {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []httpfile.Comment
	for i, para := range strings.Split(strings.TrimSpace(text), "\n") {
		if i > 0 && strings.TrimSpace(para) == "" && len(out) > 0 && out[len(out)-1].Text == "" {
			continue
		}
		if strings.TrimSpace(para) == "" {
			out = append(out, httpfile.Comment{Style: "#", Text: ""})
			continue
		}
		for _, line := range wrap(strings.TrimSpace(para), 78) {
			out = append(out, httpfile.Comment{Style: "#", Text: " " + line})
		}
	}
	return out
}

func wrap(s string, width int) []string {
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}
