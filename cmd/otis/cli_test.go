package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the CLI and returns stdout, stderr and the exit code.
func run(args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := Execute(args, &out, &errb)
	return out.String(), errb.String(), code
}

// write creates a directory tree from relative path → content.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLs(t *testing.T) {
	dir := write(t, map[string]string{
		".order":            "pets/\nhealth.http\n",
		"_folder.http":      "@baseUrl = https://x.test\n",
		"health.http":       "GET {{baseUrl}}/health\n",
		"zz-unlisted.http":  "POST {{baseUrl}}/z\n",
		"pets/.order":       "list.http\n",
		"pets/list.http":    "# @name List all pets\nGET {{baseUrl}}/pets\n",
		"pets/delete.http":  "DELETE {{baseUrl}}/pets/1\n",
		"pets/broken.http":  "GET {{baseUrl}}/x\nnot a header\n",
		"pets/_folder.http": "Accept: application/json\n",
	})
	out, errOut, code := run("ls", dir)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	// pets/.order lists list.http; broken.http and delete.http follow
	// alphabetically. The display-name column is aligned across all rows.
	want := strings.Join([]string{
		"        pets/",
		"GET       list.http       List all pets",
		"?         broken.http",
		"DELETE    delete.http",
		"GET     health.http",
		"POST    zz-unlisted.http",
	}, "\n") + "\n"
	if out != want {
		t.Errorf("output =\n%q\nwant\n%q", out, want)
	}
	if !strings.Contains(errOut, "1 warning(s):") || !strings.Contains(errOut, "pets/broken.http: parse-error") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestLsEmptyAndErrors(t *testing.T) {
	out, _, code := run("ls", t.TempDir())
	if code != ExitOK || out != "(empty collection)\n" {
		t.Errorf("out = %q, code = %d", out, code)
	}
	_, errOut, code := run("ls", filepath.Join(t.TempDir(), "nope"))
	if code != ExitProblem || !strings.Contains(errOut, "otis:") {
		t.Errorf("missing dir: code = %d, stderr = %q", code, errOut)
	}
}

// echoServer reports what it received and honours ?status= and ?redirect=.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/echo", http.StatusFound)
	})
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{0xff, 0xfe, 0x00, 0x01}) //nolint:errcheck
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if s := r.URL.Query().Get("status"); s != "" {
			var code int
			fmt.Sscanf(s, "%d", &code)
			w.WriteHeader(code)
			return
		}
		body, _ := io_ReadAll(r)
		received := map[string]string{}
		for name, vs := range r.Header {
			received[strings.ToLower(name)] = strings.Join(vs, ", ")
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"method":  r.Method,
			"path":    r.URL.RequestURI(),
			"headers": received,
			"body":    string(body),
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func io_ReadAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	var b bytes.Buffer
	_, err := b.ReadFrom(r.Body)
	return b.Bytes(), err
}

func TestRunText(t *testing.T) {
	srv := echoServer(t)
	dir := write(t, map[string]string{
		"_folder.http":    "@baseUrl = " + srv.URL + "\nAccept: application/json\n",
		"api/create.http": "# @name Create\n@id = 7\nPOST {{baseUrl}}/echo?id={{id}}\nContent-Type: application/json\n\n{\"id\": {{id}}}\n",
	})
	out, errOut, code := run("run", filepath.Join(dir, "api", "create.http"))
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	for _, want := range []string{
		"POST " + srv.URL + "/echo?id=7",
		"  Accept: application/json",
		"  Content-Type: application/json",
		"200 OK",
		"Content-Type: application/json",
		`"method": "POST"`,
		`"path": "/echo?id=7"`,
		`"accept": "application/json"`,
		`"body": "{\"id\": 7}"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if errOut != "" {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestRunJSON(t *testing.T) {
	srv := echoServer(t)
	dir := write(t, map[string]string{
		"r.http": "GET " + srv.URL + "/echo\nAccept: text/plain\n",
	})
	out, _, code := run("run", filepath.Join(dir, "r.http"), "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Request.Method != "GET" || got.Request.Headers["Accept"] != "text/plain" {
		t.Errorf("request = %+v", got.Request)
	}
	if got.Response.StatusCode != 200 || got.Response.Size == 0 || got.Response.DurationMs <= 0 {
		t.Errorf("response = %+v", got.Response)
	}
	if got.Response.Timing.TotalMs <= 0 || got.Response.FinalURL != srv.URL+"/echo" {
		t.Errorf("timing/url = %+v", got.Response)
	}
	if !strings.Contains(got.Response.Body, `"method": "GET"`) || got.Response.BodyBase64 != "" {
		t.Errorf("body = %q", got.Response.Body)
	}
}

func TestRunBinaryBody(t *testing.T) {
	srv := echoServer(t)
	dir := write(t, map[string]string{"r.http": "GET " + srv.URL + "/binary\n"})

	out, _, code := run("run", filepath.Join(dir, "r.http"), "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var got jsonOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Response.Body != "" || got.Response.BodyBase64 != "//4AAQ==" {
		t.Errorf("body = %q, base64 = %q", got.Response.Body, got.Response.BodyBase64)
	}
	out, _, _ = run("run", filepath.Join(dir, "r.http"))
	if !strings.Contains(out, "(4 B of binary data)") {
		t.Errorf("text output = %q", out)
	}
}

func TestRunExitCodes(t *testing.T) {
	srv := echoServer(t)
	tests := []struct {
		name string
		file string
		args []string
		code int
		want string
	}{
		{"ok", "GET " + srv.URL + "/echo\n", nil, ExitOK, ""},
		{"404", "GET " + srv.URL + "/x?status=404\n", nil, ExitFailed, ""},
		{"500", "GET " + srv.URL + "/x?status=500\n", nil, ExitFailed, ""},
		{"connection refused", "GET http://127.0.0.1:1/x\n", nil, ExitProblem, "otis:"},
		{"unresolved variable", "GET {{nope}}/x\n", nil, ExitProblem, "unresolved variables: nope"},
		{"broken file", "GET " + srv.URL + "\nnot a header\n", nil, ExitProblem, "line 2"},
		{"unknown environment", "GET " + srv.URL + "/echo\n", []string{"-e", "nope"}, ExitProblem, `environment "nope" not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := write(t, map[string]string{"r.http": tt.file})
			args := append([]string{"run", filepath.Join(dir, "r.http")}, tt.args...)
			_, errOut, code := run(args...)
			if code != tt.code {
				t.Errorf("code = %d, want %d (stderr %q)", code, tt.code, errOut)
			}
			if tt.want != "" && !strings.Contains(errOut, tt.want) {
				t.Errorf("stderr = %q, want %q", errOut, tt.want)
			}
			if tt.code == ExitFailed && errOut != "" {
				t.Errorf("failed request should not print an error: %q", errOut)
			}
		})
	}
}

func TestRunUsageErrors(t *testing.T) {
	dir := write(t, map[string]string{"a.http": "GET https://x.test\n", "notes.txt": "x"})
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing arg", []string{"run"}, "accepts 1 arg"},
		{"missing file", []string{"run", filepath.Join(dir, "nope.http")}, "no such file"},
		{"directory", []string{"run", dir}, "is a directory"},
		{"not a request", []string{"run", filepath.Join(dir, "notes.txt")}, "not a request"},
		{"unknown command", []string{"nope"}, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errOut, code := run(tt.args...)
			if code != ExitProblem || !strings.Contains(errOut, tt.want) {
				t.Errorf("code = %d, stderr = %q, want %q", code, errOut, tt.want)
			}
		})
	}
}

func TestRunRedirectsAndFlags(t *testing.T) {
	srv := echoServer(t)
	dir := write(t, map[string]string{"r.http": "GET " + srv.URL + "/redirect\n"})
	out, _, code := run("run", filepath.Join(dir, "r.http"))
	if code != ExitOK || !strings.Contains(out, "-> 302 "+srv.URL+"/echo") {
		t.Errorf("code = %d, out = %s", code, out)
	}
	out, _, code = run("run", filepath.Join(dir, "r.http"), "--no-redirect")
	if code != ExitOK || !strings.Contains(out, "302 Found") || strings.Contains(out, "-> 302") {
		t.Errorf("no-redirect: code = %d, out = %s", code, out)
	}
	_, errOut, code := run("run", filepath.Join(dir, "r.http"), "--timeout", "1ns")
	if code != ExitProblem || !strings.Contains(errOut, "timed out") {
		t.Errorf("timeout: code = %d, stderr = %q", code, errOut)
	}
}

func TestRunEnvironmentAndSecrets(t *testing.T) {
	srv := echoServer(t)
	files := map[string]string{
		"env/dev.json": `{"baseUrl": ` + jsonString(srv.URL) + `, "token": {"$secret": "keychain"}}`,
		"r.http":       "# @auth bearer {{token}}\nGET {{baseUrl}}/echo\n",
	}
	t.Run("secret supplied by environment variable", func(t *testing.T) {
		dir := write(t, files)
		t.Setenv(SecretEnvPrefix+"TOKEN", "s3cr3t")
		out, errOut, code := run("run", filepath.Join(dir, "r.http"), "-e", "dev")
		if code != ExitOK {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		// The printed request echo is masked...
		if !strings.Contains(out, "  Authorization: Bearer •••••") || strings.Contains(out, "  Authorization: Bearer s3cr3t") {
			t.Errorf("request headers not masked:\n%s", out)
		}
		// ...but the server really received the secret, so masking is
		// presentation only and never changes what is sent.
		if !strings.Contains(out, `"authorization": "Bearer s3cr3t"`) {
			t.Errorf("server did not receive the secret:\n%s", out)
		}
	})
	t.Run("lenient secret name matching", func(t *testing.T) {
		dir := write(t, map[string]string{
			"env/dev.json": `{"baseUrl": ` + jsonString(srv.URL) + `, "api-key": {"$secret": "keychain"}}`,
			"r.http":       "GET {{baseUrl}}/echo\nX-Key: {{api-key}}\n",
		})
		t.Setenv(SecretEnvPrefix+"API_KEY", "abc")
		out, errOut, code := run("run", filepath.Join(dir, "r.http"), "-e", "dev", "--json")
		if code != ExitOK {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		var got jsonOutput
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if got.Request.Headers["X-Key"] != "•••••" {
			t.Errorf("X-Key not masked in output: %q", got.Request.Headers["X-Key"])
		}
		if !strings.Contains(got.Response.Body, `"x-key": "abc"`) {
			t.Errorf("OTIS_SECRET_API_KEY did not reach the variable api-key:\n%s", got.Response.Body)
		}
	})
	t.Run("camelCase secret name", func(t *testing.T) {
		dir := write(t, map[string]string{
			"env/dev.json": `{"baseUrl": ` + jsonString(srv.URL) + `, "apiToken": {"$secret": "keychain"}}`,
			"r.http":       "# @auth bearer {{apiToken}}\nGET {{baseUrl}}/echo\n",
		})
		t.Setenv(SecretEnvPrefix+"API_TOKEN", "tok")
		out, errOut, code := run("run", filepath.Join(dir, "r.http"), "-e", "dev")
		if code != ExitOK {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if !strings.Contains(out, `"authorization": "Bearer tok"`) {
			t.Errorf("OTIS_SECRET_API_TOKEN did not supply apiToken:\n%s", out)
		}
	})
	t.Run("missing secret names the key and the variable that supplies it", func(t *testing.T) {
		dir := write(t, files)
		_, errOut, code := run("run", filepath.Join(dir, "r.http"), "-e", "dev")
		if code != ExitProblem || !strings.Contains(errOut, `secret "token"`) || !strings.Contains(errOut, "not set in the secret store") {
			t.Errorf("code = %d, stderr = %q", code, errOut)
		}
		if !strings.Contains(errOut, "set "+SecretEnvPrefix+"TOKEN to supply it") {
			t.Errorf("stderr has no hint: %q", errOut)
		}
		if strings.Contains(errOut, "never-exported") {
			t.Error("stderr leaked a secret value")
		}
	})
	t.Run("environment list in the error", func(t *testing.T) {
		dir := write(t, files)
		_, errOut, _ := run("run", filepath.Join(dir, "r.http"), "-e", "prod")
		if !strings.Contains(errOut, "available: dev") {
			t.Errorf("stderr = %q", errOut)
		}
	})
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestFindRoot(t *testing.T) {
	dir := write(t, map[string]string{
		"repo/.git/HEAD":              "ref: x\n",
		"repo/api/env/dev.json":       "{}",
		"repo/api/_folder.http":       "@a = 1\n",
		"repo/api/users/_folder.http": "@b = 2\n",
		"repo/api/users/create.http":  "GET https://x.test\n",
		"repo/other/loose.http":       "GET https://x.test\n",
		"standalone/a/b/.order":       "c.http\n",
		"standalone/a/b/c.http":       "GET https://x.test\n",
		"standalone/a/_folder.http":   "@x = 1\n",
	})
	tests := map[string]string{
		"repo/api/users": "repo/api",   // stops at env/
		"repo/api":       "repo/api",   // env/ here
		"repo/other":     "repo/other", // no markers above
		"standalone/a/b": "standalone/a",
	}
	for start, want := range tests {
		got := FindRoot(filepath.Join(dir, filepath.FromSlash(start)))
		if got != filepath.Join(dir, filepath.FromSlash(want)) {
			t.Errorf("FindRoot(%s) = %s, want %s", start, got, filepath.Join(dir, want))
		}
	}
	// Inheritance really follows the discovered root.
	srv := echoServer(t)
	d2 := write(t, map[string]string{
		"env/dev.json":   `{"baseUrl": ` + jsonString(srv.URL) + `}`,
		"_folder.http":   "X-Root: yes\n",
		"a/_folder.http": "Accept: text/plain\n",
		"a/b/r.http":     "GET {{baseUrl}}/echo\n",
	})
	out, errOut, code := run("run", filepath.Join(d2, "a", "b", "r.http"), "-e", "dev")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "  X-Root: yes") || !strings.Contains(out, `"accept": "text/plain"`) {
		t.Errorf("inheritance did not reach the root:\n%s", out)
	}
	// -C overrides discovery: rooted at a/, X-Root is not inherited.
	out, _, code = run("run", filepath.Join(d2, "a", "b", "r.http"), "-C", filepath.Join(d2, "a"), "-e", "dev")
	if code != ExitProblem {
		// baseUrl lives in the root env/, which -C hides.
		t.Errorf("code = %d, out = %s", code, out)
	}
}

func TestImportPostman(t *testing.T) {
	src := filepath.Join("..", "..", "internal", "importer", "postman", "testdata", "postman")
	out := filepath.Join(t.TempDir(), "collection")

	stdout, stderr, code := run("import", "postman",
		filepath.Join(src, "petstore.postman_collection.json"),
		"-o", out,
		"--env", filepath.Join(src, "dev.postman_environment.json"))
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "Wrote 24 files to") {
		t.Errorf("stdout = %q", stdout)
	}
	for _, want := range []string{`Imported "Petstore API": 13 requests`, "Skipped (", "Needs attention (", "keychain references"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("report missing %q:\n%s", want, stderr)
		}
	}
	// The imported tree is immediately listable.
	lsOut, lsErr, code := run("ls", out)
	if code != ExitOK || lsErr != "" {
		t.Fatalf("ls: code = %d, stderr = %q", code, lsErr)
	}
	for _, want := range []string{"pets/", "GET       list-pets.http", "List pets", "health-check.http"} {
		if !strings.Contains(lsOut, want) {
			t.Errorf("ls output missing %q:\n%s", want, lsOut)
		}
	}
	// Re-importing refuses to clobber.
	_, _, code = run("import", "postman", filepath.Join(src, "petstore.postman_collection.json"), "-o", out)
	if code != ExitProblem {
		t.Errorf("second import: code = %d, want %d", code, ExitProblem)
	}
	_, _, code = run("import", "postman", filepath.Join(src, "petstore.postman_collection.json"), "-o", out, "--force")
	if code != ExitOK {
		t.Errorf("forced import: code = %d", code)
	}
	// Missing -o is a usage error.
	_, stderr, code = run("import", "postman", filepath.Join(src, "petstore.postman_collection.json"))
	if code != ExitProblem || !strings.Contains(stderr, "output directory is required") {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestVersionAndHelp(t *testing.T) {
	Version = "1.2.3"
	t.Cleanup(func() { Version = "dev" })
	out, _, code := run("version")
	if code != ExitOK || out != "1.2.3\n" {
		t.Errorf("version = %q, code = %d", out, code)
	}
	out, _, code = run("--help")
	if code != ExitOK || !strings.Contains(out, "Available Commands:") {
		t.Errorf("help: code = %d", code)
	}
}

func TestFormatting(t *testing.T) {
	sizes := map[int64]string{0: "0 B", 512: "512 B", 1024: "1.0 kB", 1536: "1.5 kB", 5 << 20: "5.0 MB"}
	for n, want := range sizes {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", n, got, want)
		}
	}
	names := map[string]string{
		"apiToken":     "API_TOKEN",
		"api-key.v2":   "API_KEY_V2",
		"API_TOKEN":    "API_TOKEN",
		"token":        "TOKEN",
		"ApiKey":       "API_KEY",
		"HTTPToken":    "HTTP_TOKEN",
		"awsSecretKey": "AWS_SECRET_KEY",
		"a":            "A",
		"__x__":        "X",
		"":             "",
	}
	for in, want := range names {
		if got := normalizeSecretName(in); got != want {
			t.Errorf("normalizeSecretName(%q) = %q, want %q", in, got, want)
		}
	}
}
