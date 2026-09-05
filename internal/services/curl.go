package services

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/otis-http/otis/internal/collection"
	"github.com/otis-http/otis/internal/curl"
	"github.com/otis-http/otis/internal/httpclient"
	"github.com/otis-http/otis/internal/httpfile"
	"github.com/otis-http/otis/internal/resolve"
	"github.com/otis-http/otis/internal/scriptrun"
)

// cURL, in both directions.
//
// One file rather than a method on each of two services, because the pair is
// one feature and the interesting decisions are shared: what a curl command
// and a `.http` file each can say, and which of the two is allowed to carry a
// secret.

// CurlPlan is what an imported command would become, before it is written.
//
// The `.http` **text** rather than a parsed model: what a person needs to
// agree to is the file, and showing them a rendering of a model would be
// showing them something one serializer away from the truth. This is the
// output of the same serializer that will write it.
type CurlPlan struct {
	// Name is the display name derived from the URL, which the dialog
	// prefills and the person can change.
	Name string `json:"name"`
	// Text is the file as it will be written.
	Text string `json:"text"`
	// Notes are the parts of the command that were not translated. They are
	// in Text as comments too; this is for showing them separately.
	Notes []string `json:"notes"`
	// Problem is why the command cannot be imported, or "".
	Problem string `json:"problem"`
}

// PlanCurl parses a command and reports what importing it would write.
//
// It never fails: a command that cannot be parsed comes back as a Problem,
// because this runs on every keystroke in a paste box and a rejected promise
// per character is not an error report, it is noise.
func (s *RequestService) PlanCurl(command string) CurlPlan {
	if strings.TrimSpace(command) == "" {
		return CurlPlan{}
	}
	result, err := curl.Parse(command)
	if err != nil {
		return CurlPlan{Problem: err.Error()}
	}
	return CurlPlan{
		Name:  result.Name,
		Text:  (&httpfile.File{Requests: []*httpfile.Request{result.Request}}).String(),
		Notes: result.Notes,
	}
}

// CreateFromCurl writes a command into a new request and returns its node path.
//
// The command is parsed again here rather than taking the plan's text from the
// window: a plan is a preview, and the file Otis writes is always the output
// of Go's own serializer over Go's own model (CLAUDE.md). The typed name wins
// over the derived one, and it names the file too, exactly as Create does.
func (s *RequestService) CreateFromCurl(folderPath, name, command string) (string, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return "", err
	}
	folder := loaded.Find(folderPath)
	if folder == nil || folder.Kind != collection.KindFolder {
		return "", fmt.Errorf("%s is not a folder in this collection", displayPath(folderPath))
	}
	result, err := curl.Parse(command)
	if err != nil {
		return "", err
	}
	file := &httpfile.File{Requests: []*httpfile.Request{result.Request}}
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		setRequestName(file, trimmed)
		result.Name = trimmed
	}

	used, err := namesInUse(folder.Path)
	if err != nil {
		return "", err
	}
	base := collection.UniqueName(used, collection.Slug(result.Name), "request")
	nodePath := path.Join(folderPath, base+collection.RequestExt)
	if _, err := s.write(nodePath, "", file.String()); err != nil {
		return "", err
	}
	return nodePath, nil
}

// CopyAsCurl puts the request on the clipboard as a `curl` command.
//
// # Why the sender and not the editor
//
// What is worth copying is the request as it will actually be *sent*:
// inherited headers, the environment's values, the `Authorization` an
// `@auth` line becomes. A command built from the file would carry
// `{{baseUrl}}` and none of the inheritance, which is a command that does not
// run and a lie about what Otis does.
//
// # Secrets
//
// `withSecrets` is a decision only the caller can make, so it is a parameter
// and not a policy here. Masked, the command is safe to paste into an issue
// and will not run; unmasked it runs and carries a credential. Both are
// legitimate and the window offers them as two separate items, so the choice
// is made by the person, in words, at the moment they make it.
//
// The value goes to the clipboard **from Go** and never crosses the binding
// (CLAUDE.md), which is the same path `EnvironmentService.CopySecretValue`
// takes and for the same reason.
//
// Scripts do not run. A pre-request hook can change the request, so a command
// built without running one can differ from what a send would produce — but
// running somebody's script as a side effect of a copy is worse, and a hook
// may write session variables or hit the network through nothing at all.
// Where there is one, the command says so in a comment.
func (s *SendService) CopyAsCurl(nodePath, envName string, withSecrets bool) error {
	command, err := s.curlCommand(nodePath, envName, withSecrets)
	if err != nil {
		return err
	}
	if s.app == nil {
		return fmt.Errorf("no window to copy from")
	}
	if !s.app.Clipboard.SetText(command) {
		return fmt.Errorf("the clipboard refused the command")
	}
	return nil
}

// curlCommand builds the command CopyAsCurl copies.
//
// **Unexported, and it must stay that way.** Wails binds every exported
// method of a registered service to the window, and with `withSecrets` this
// returns a string containing a resolved credential — the one thing that
// never crosses the binding (CLAUDE.md). The exported method above takes the
// same arguments and returns nothing but an error; the value goes to the
// clipboard from Go.
func (s *SendService) curlCommand(nodePath, envName string, withSecrets bool) (string, error) {
	loaded, err := s.collections.Loaded()
	if err != nil {
		return "", err
	}
	node := loaded.Find(nodePath)
	if node == nil || node.Kind != collection.KindRequest || node.Request == nil {
		return "", fmt.Errorf("%s is not a request in this collection", displayPath(nodePath))
	}

	var env *resolve.Environment
	if envName != "" {
		if env, err = resolve.LoadEnvironment(loaded.Dir, envName); err != nil {
			return "", fmt.Errorf("reading the environment: %w", err)
		}
	}
	res, err := resolve.InCollection(loaded, node, resolve.Options{
		Env:     env,
		Secrets: s.secrets,
		Session: s.vars,
	})
	if err != nil {
		// Masked whatever happens: a resolve error can quote a value.
		if res != nil {
			return "", fmt.Errorf("%s", res.Mask(err.Error()))
		}
		return "", err
	}

	req, _, err := httpclient.Prepare(
		context.Background(), res, node.Request, filepath.Dir(node.Path), httpclient.PrepareOptions{})
	if err != nil {
		return "", fmt.Errorf("%s", res.Mask(err.Error()))
	}

	command := curl.Format(req)
	if plan := scriptrun.PlanFor(loaded, node); len(plan.Pre) > 0 {
		command += "\n# note: a pre-request script runs before this in Otis, and is not part of this command."
	}
	if !withSecrets {
		command = res.Mask(command)
	}
	return command, nil
}
