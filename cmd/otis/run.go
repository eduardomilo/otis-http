package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/httpclient"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/script"
	"github.com/otis-http/otis/internal/scriptrun"
	"github.com/otis-http/otis/internal/secrets"
)

func newRunCmd() *cobra.Command {
	var (
		envName    string
		asJSON     bool
		rootDir    string
		timeout    time.Duration
		noRedirect bool
		insecure   bool
	)
	cmd := &cobra.Command{
		Use:   "run <file.http>",
		Short: "Resolve and send one request",
		Long: "Resolve a request file (inheritance, variables, auth) and send it.\n\n" +
			"Secret values referenced by the environment are read from " + SecretEnvPrefix + "*\n" +
			"environment variables; " + SecretEnvPrefix + "API_KEY supplies apiKey, api-key or\n" +
			"api.key. Resolved request headers are printed masked.\n\n" +
			"Exits 1 when the response status is 4xx or 5xx, 2 on any other problem.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRequest(cmd, args[0], runOptions{
				env: envName, asJSON: asJSON, root: rootDir,
				timeout: timeout, noRedirect: noRedirect, insecure: insecure,
			})
		},
	}
	f := cmd.Flags()
	f.StringVarP(&envName, "env", "e", "", "environment to resolve against (env/<name>.json)")
	f.BoolVar(&asJSON, "json", false, "print one JSON object instead of text")
	f.StringVarP(&rootDir, "collection", "C", "", "collection root (default: discovered from the file)")
	f.DurationVar(&timeout, "timeout", 0, "override the request timeout, e.g. 5s")
	f.BoolVar(&noRedirect, "no-redirect", false, "do not follow redirects")
	f.BoolVar(&insecure, "insecure", false, "skip TLS certificate verification")
	return cmd
}

type runOptions struct {
	env        string
	asJSON     bool
	root       string
	timeout    time.Duration
	noRedirect bool
	insecure   bool
}

func runRequest(cmd *cobra.Command, file string, opts runOptions) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil {
		return err
	} else if st.IsDir() {
		return fmt.Errorf("%s is a directory; give a .http file", file)
	}
	root := opts.root
	if root == "" {
		root = FindRoot(filepath.Dir(abs))
	}
	c, err := collection.Load(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(c.Dir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%s is not inside the collection at %s", file, c.Dir)
	}
	node := c.Find(filepath.ToSlash(rel))
	if node == nil || node.Kind != collection.KindRequest {
		return fmt.Errorf("%s is not a request in the collection at %s", file, c.Dir)
	}
	if node.Broken {
		return fmt.Errorf("%s: %s", rel, node.Error)
	}
	if node.Request == nil {
		return fmt.Errorf("%s contains no request", rel)
	}
	errw := cmd.ErrOrStderr()
	for _, w := range c.Warnings {
		if w.Path == node.ID {
			fmt.Fprintf(errw, "warning: %s\n", w)
		}
	}

	env, store, missingSecrets, err := loadEnv(c, opts.env)
	if err != nil {
		return err
	}
	// The window and the CLI run scripts the same way, through the same
	// package: `otis run` in CI and the request editor are the same binary,
	// and a request whose scripts ran in one and not the other would make
	// this a different product with the same name.
	plan := scriptrun.PlanFor(c, node)
	budget, err := script.TimeoutOf(node.Request)
	if err != nil {
		return fmt.Errorf("%s: %w", node.ID, err)
	}
	sv := cliVars(c, node, env, store)
	var preResult script.Result
	var preErr error

	res, err := resolve.InCollection(c, node, resolve.Options{
		Env: env, Secrets: store, Session: sv.Session, Extra: sv.Request,
		Rewrite: func(entry *httpfile.Request, eff *resolve.Effective) {
			if len(plan.Pre) == 0 {
				return
			}
			view := scriptrun.RequestView(node, entry, eff)
			preResult, preErr = script.Run(script.Options{
				Phase: script.Pre, Scripts: plan.Pre, Request: view, Vars: sv,
				Secrets: store, Env: env, Collection: resolve.CollectionKey(c),
				Modules: scriptrun.ModuleLoader{Root: c.Dir}, Timeout: budget,
				OnConsole: func(line script.ConsoleLine) { printConsole(errw, line) },
			})
			if preErr == nil && view.Changed() {
				scriptrun.ApplyView(entry, eff, view)
			}
		},
	})
	if preErr != nil {
		return preErr
	}
	if err != nil {
		return secretEnvHint(err, missingSecrets)
	}

	session := httpclient.NewSession()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	req, warnings, err := httpclient.Prepare(ctx, res, node.Request, filepath.Dir(node.Path), session.PrepareOptions())
	if err != nil {
		return err
	}
	scriptrun.SubstituteHandles(res, req, preResult.Handles)
	for _, w := range warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	if opts.noRedirect {
		req.Options.NoRedirect = true
	}
	if opts.timeout > 0 {
		req.Options.Timeout = opts.timeout
	}

	client := &httpclient.Client{Session: session, InsecureSkipVerify: opts.insecure}
	resp, err := client.Do(ctx, req)
	if err != nil {
		return err
	}
	// Post-response scripts and their tests. A script that throws here does
	// not discard the response (docs/FORMAT.md §9.10), so the body is printed
	// either way and the failure goes to stderr.
	var tests []script.TestResult
	var postErr error
	if len(plan.Post) > 0 {
		postResult, err := script.Run(script.Options{
			Phase: script.Post, Scripts: plan.Post,
			Request: scriptrun.SentView(node, res, req),
			Response: &script.ResponseView{
				Status: resp.StatusCode, StatusText: scriptrun.StatusText(resp.Status),
				Headers: scriptrun.ResponseHeaders(resp.Headers), Body: string(resp.Body),
				Size: resp.Size,
			},
			Vars: sv, Secrets: store, Env: env, Collection: resolve.CollectionKey(c),
			Modules: scriptrun.ModuleLoader{Root: c.Dir}, Timeout: budget,
			OnConsole: func(line script.ConsoleLine) { printConsole(errw, line) },
		})
		tests, postErr = postResult.Tests, err
	}

	if opts.asJSON {
		if err := printJSON(cmd.OutOrStdout(), res, req, resp, tests); err != nil {
			return err
		}
	} else {
		printText(cmd.OutOrStdout(), res, req, resp)
		printTests(cmd.OutOrStdout(), tests)
	}
	if postErr != nil {
		fmt.Fprintf(errw, "script error: %v\n", postErr)
	}
	// A failing test fails the run, the same way a 4xx does: the point of an
	// assertion in CI is that the exit code changes when it does not hold.
	if failed := failedTests(tests); failed > 0 {
		return &failedError{status: fmt.Sprintf("%d of %d tests failed", failed, len(tests))}
	}
	if postErr != nil {
		return &failedError{status: "a post-response script failed"}
	}
	if resp.IsError() {
		return &failedError{status: resp.Status}
	}
	return nil
}

// cliVars is the variable store a CLI run's scripts write through.
//
// The session store is fresh per invocation and forgotten when the process
// exits, which is the honest reading of "until the collection closes" for a
// one-shot command. vars.env.set still writes the file: that is the scope
// whose whole point is that it persists.
func cliVars(c *collection.Collection, node *collection.Node, env *resolve.Environment, store secrets.Store) *scriptrun.Vars {
	sv := &scriptrun.Vars{
		Request: map[string]string{},
		Folder:  path.Dir(node.ID),
		Session: resolve.NewSession(),
		Origin:  node.ID,
	}
	if sv.Folder == "." {
		sv.Folder = ""
	}
	sv.Scope = func() *resolve.Scope {
		levels := resolve.LevelsOf(node)
		scope := resolve.NewScope(levels, env, secrets.Placeholder{}, resolve.CollectionKey(c))
		return scope.WithSession(sv.Session, resolve.FolderChain(node.ID)).WithExtra(sv.Request)
	}
	if env != nil {
		sv.EnvName = env.Name
		sv.ReadEnv = func(name string) (string, bool) {
			v, ok := env.Values[name]
			if !ok || v.Secret {
				return "", false
			}
			return v.Value, true
		}
		sv.WriteEnv = func(name, value string) error {
			current, err := resolve.LoadEnvironment(c.Dir, env.Name)
			if err != nil {
				return err
			}
			if existing, ok := current.Values[name]; ok && existing.Secret {
				return fmt.Errorf("%s declares %q as a secret; a script cannot overwrite one with a plain value",
					current.Path, name)
			}
			if _, ok := current.Values[name]; !ok {
				current.Order = append(current.Order, name)
			}
			current.Values[name] = resolve.EnvValue{Value: value, Kind: resolve.EnvString}
			return os.WriteFile(filepath.Join(c.Dir, filepath.FromSlash(current.Path)), current.Marshal(), 0o644)
		}
	}
	return sv
}

// printConsole writes a script's console output to stderr, so stdout stays
// the response and a pipeline is unaffected by a script that logs.
func printConsole(w io.Writer, line script.ConsoleLine) {
	fmt.Fprintf(w, "%s: %s\n", line.Level, line.Text)
}

// printTests writes the assertions, passes and failures alike.
func printTests(w io.Writer, tests []script.TestResult) {
	if len(tests) == 0 {
		return
	}
	fmt.Fprintln(w)
	for _, test := range tests {
		mark := "ok  "
		if !test.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "%s %s\n", mark, test.Name)
		if test.Message != "" {
			fmt.Fprintf(w, "     %s\n", test.Message)
		}
	}
	fmt.Fprintf(w, "\n%d of %d tests passed\n", len(tests)-failedTests(tests), len(tests))
}

func failedTests(tests []script.TestResult) int {
	n := 0
	for _, test := range tests {
		if !test.Passed {
			n++
		}
	}
	return n
}

func printText(w io.Writer, res *resolve.Resolved, req *httpclient.Request, resp *httpclient.Response) {
	fmt.Fprintf(w, "%s %s\n", req.Method, res.Mask(req.URL))
	for _, h := range req.Headers {
		fmt.Fprintf(w, "  %s: %s\n", h.Name, res.Mask(h.Value))
	}
	for _, r := range resp.Redirects {
		fmt.Fprintf(w, "  -> %d %s\n", r.StatusCode, r.Location)
	}
	fmt.Fprintf(w, "\n%s  %s  %s\n\n", resp.Status, formatDuration(resp.Duration), formatSize(resp.Size))
	names := make([]string, 0, len(resp.Headers))
	for name := range resp.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, v := range resp.Headers[name] {
			fmt.Fprintf(w, "%s: %s\n", name, v)
		}
	}
	if len(resp.Body) == 0 {
		return
	}
	fmt.Fprintln(w)
	if utf8.Valid(resp.Body) {
		w.Write(resp.Body) //nolint:errcheck
		if resp.Body[len(resp.Body)-1] != '\n' {
			fmt.Fprintln(w)
		}
		return
	}
	fmt.Fprintf(w, "(%s of binary data)\n", formatSize(resp.Size))
}

// jsonOutput is the --json shape.
type jsonOutput struct {
	Request  jsonRequest  `json:"request"`
	Response jsonResponse `json:"response"`
	// Tests are the assertions the post-response scripts declared
	// (docs/FORMAT.md §9.9), omitted when there were none.
	Tests []script.TestResult `json:"tests,omitempty"`
}

type jsonRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type jsonResponse struct {
	Status     string              `json:"status"`
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Size       int64               `json:"size"`
	DurationMs float64             `json:"durationMs"`
	Timing     jsonTiming          `json:"timing"`
	Redirects  []jsonRedirect      `json:"redirects,omitempty"`
	FinalURL   string              `json:"finalUrl"`
	// Body is the response body when it is valid UTF-8, else BodyBase64.
	Body       string `json:"body,omitempty"`
	BodyBase64 string `json:"bodyBase64,omitempty"`
}

type jsonTiming struct {
	DNSMs     float64 `json:"dnsMs"`
	ConnectMs float64 `json:"connectMs"`
	TLSMs     float64 `json:"tlsMs"`
	TTFBMs    float64 `json:"ttfbMs"`
	TotalMs   float64 `json:"totalMs"`
}

type jsonRedirect struct {
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode"`
	Location   string `json:"location"`
}

func printJSON(w io.Writer, res *resolve.Resolved, req *httpclient.Request, resp *httpclient.Response, tests []script.TestResult) error {
	out := jsonOutput{
		// Tests are in the JSON too: a CI job that wants the verdicts rather
		// than the exit code alone should not have to parse the text form.
		Tests: tests,
		Request: jsonRequest{
			Method:  req.Method,
			URL:     res.Mask(req.URL),
			Headers: map[string]string{},
		},
		Response: jsonResponse{
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Headers:    map[string][]string(http.Header(resp.Headers)),
			Size:       resp.Size,
			DurationMs: ms(resp.Duration),
			Timing: jsonTiming{
				DNSMs:     ms(resp.Timing.DNS),
				ConnectMs: ms(resp.Timing.Connect),
				TLSMs:     ms(resp.Timing.TLS),
				TTFBMs:    ms(resp.Timing.TTFB),
				TotalMs:   ms(resp.Timing.Total),
			},
			FinalURL: resp.FinalURL,
		},
	}
	for _, h := range req.Headers {
		name := h.Name
		if prev, ok := out.Request.Headers[name]; ok {
			out.Request.Headers[name] = prev + ", " + res.Mask(h.Value)
			continue
		}
		out.Request.Headers[name] = res.Mask(h.Value)
	}
	for _, r := range resp.Redirects {
		out.Response.Redirects = append(out.Response.Redirects, jsonRedirect{r.URL, r.StatusCode, r.Location})
	}
	if utf8.Valid(resp.Body) {
		out.Response.Body = string(resp.Body)
	} else {
		out.Response.BodyBase64 = base64.StdEncoding.EncodeToString(resp.Body)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < 10*time.Second:
		return fmt.Sprintf("%.0fms", ms(d))
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}
