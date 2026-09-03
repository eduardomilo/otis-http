// Package cli implements the otis command line. It lives in the same binary
// as the GUI: main.go dispatches here when the process is started with
// arguments and opens the window otherwise.
//
// Exit codes:
//
//	0  success (and, for run, a response with status < 400)
//	1  the server answered with 4xx or 5xx
//	2  anything else: usage, parse, resolve, network or timeout errors
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/otis-http/otis/internal/buildinfo"
	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/secrets"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitProblem = 2
)

// SecretEnvPrefix is the environment-variable prefix that supplies secret
// values to the CLI, e.g. OTIS_SECRET_API_TOKEN for the variable apiToken.
const SecretEnvPrefix = "OTIS_SECRET_"

// failedError marks a request that got a 4xx or 5xx response: reported, but
// with its own exit code and no error text.
type failedError struct{ status string }

func (e *failedError) Error() string { return e.status }

// Execute runs the command line with the given arguments (without argv[0])
// and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var failed *failedError
	if errors.As(err, &failed) {
		return ExitFailed
	}
	fmt.Fprintln(stderr, "otis:", err)
	return ExitProblem
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "otis",
		Short: "File-based HTTP client",
		Long: "Otis is a file-based HTTP client. A collection is a directory of .http\n" +
			"files in your repository.\n\nRun otis with no arguments to open the desktop app.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// `otis --version` and `otis version` print the same block. Cobra
		// gives the flag for free once Version is set; the template is
		// overridden because the default one prefixes "otis version " to it.
		Version: buildinfo.Version,
	}
	root.SetVersionTemplate(buildinfo.Block())
	root.AddCommand(newLsCmd(), newRunCmd(), newImportCmd(), newVersionCmd())
	return root
}

// newVersionCmd prints the build identity: version, commit, build date,
// toolchain and target.
//
// All five, not just the version, because with no auto-updater the answer to
// "what are you running" has to be complete enough to paste into a bug report
// (docs/RELEASING.md). A build that was not stamped by the release pipeline
// says so, rather than printing blanks.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Otis version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprint(cmd.OutOrStdout(), buildinfo.Block())
			return nil
		},
	}
}

// loadEnv loads the named environment from the collection and builds the
// secret store: OTIS_SECRET_* environment variables first, then this machine's
// OS keychain (docs/FORMAT.md §5).
//
// That order is the point. CI has no keychain and supplies secrets as
// environment variables, so those must work with none present; a developer
// running `otis run` locally has the values the window put in the keychain and
// should not have to export them again. Environment variables winning also
// means a one-off override is a prefix on the command line, which is what
// anybody would expect.
//
// Names are matched leniently: both the suffix and the variable name are
// converted to SCREAMING_SNAKE_CASE, so OTIS_SECRET_API_KEY supplies apiKey,
// api-key, api.key, API_KEY or ApiKey.
//
// Secrets that neither source supplies are returned in missing (as the
// variable names). That is not fatal: the request may never reference them.
// secretEnvHint turns one into advice if resolution does fail.
func loadEnv(c *collection.Collection, name string) (*resolve.Environment, secrets.Store, []string, error) {
	if name == "" {
		return nil, nil, nil, nil
	}
	env, err := resolve.LoadEnvironment(c.Dir, name)
	if err != nil {
		names, _ := resolve.ListEnvironments(c.Dir)
		if len(names) > 0 {
			return nil, nil, nil, fmt.Errorf("%w (available: %s)", err, strings.Join(names, ", "))
		}
		return nil, nil, nil, err
	}
	supplied := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, SecretEnvPrefix) {
			continue
		}
		supplied[normalizeSecretName(strings.TrimPrefix(k, SecretEnvPrefix))] = v
	}
	fromEnv := secrets.NewMemory()
	var store secrets.Store = fromEnv
	if index, err := secrets.DefaultIndexPath(); err == nil {
		store = secrets.Fallback{fromEnv, secrets.NewKeyring(index)}
	}

	var missing []string
	for _, varName := range env.SecretNames() {
		key := secrets.Key(resolve.CollectionKey(c), env.Name, varName)
		if v, ok := supplied[normalizeSecretName(varName)]; ok {
			if err := fromEnv.Set(key, v); err != nil {
				return nil, nil, nil, err
			}
			continue
		}
		// Not supplied by the environment. The keychain may still have it,
		// but asking now would prompt for every secret in the file whether
		// the request references it or not, so this only records that no
		// environment variable named it — the resolver's own lookup, which
		// happens for the secrets actually used, goes through the fallback.
		missing = append(missing, varName)
	}
	return env, store, missing, nil
}

// secretEnvHint names the environment variable that would have supplied a
// secret the resolver could not find.
func secretEnvHint(err error, missing []string) error {
	var se *resolve.SecretError
	if !errors.As(err, &se) {
		return err
	}
	for _, name := range missing {
		if name == se.Name {
			return fmt.Errorf("%w\n       set %s%s to supply it", err, SecretEnvPrefix, normalizeSecretName(name))
		}
	}
	return err
}

// normalizeSecretName converts a variable name to SCREAMING_SNAKE_CASE:
// camelCase boundaries become underscores, as do runs of any other
// character, and the result is upper-cased.
func normalizeSecretName(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prev := runes[i-1]
			prevLowerOrDigit := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
			prevUpper := prev >= 'A' && prev <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			// apiToken -> API_TOKEN, HTTPToken -> HTTP_TOKEN
			if prevLowerOrDigit || (prevUpper && nextLower) {
				b.WriteByte('_')
			}
		}
		switch {
		case isUpper, r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		default:
			b.WriteByte('_')
		}
	}
	// Collapse runs of underscores and trim the ends.
	out := b.String()
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return strings.Trim(out, "_")
}

// formatSize renders a byte count for humans.
func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
