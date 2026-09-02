package main

import (
	"embed"
	"log"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	cli "github.com/otis-http/otis/cmd/otis"
	"github.com/otis-http/otis/internal/events"
	"github.com/otis-http/otis/internal/git"
	"github.com/otis-http/otis/internal/secrets"
	"github.com/otis-http/otis/internal/services"
	"github.com/otis-http/otis/internal/settings"
)

// The production frontend build is embedded from frontend/dist.
// Do not change the Vite build.outDir; this path depends on it.
//
//go:embed all:frontend/dist
var assets embed.FS

// Window geometry. The design is drawn at 1440x900 (DESIGN-NOTES §4.1); the
// minimum is the point below which the three panes stop making sense.
const (
	windowWidth     = 1440
	windowHeight    = 900
	windowMinWidth  = 960
	windowMinHeight = 600
)

func init() {
	// Registering each event gives the generated TS bindings its payload
	// type. The names come from internal/events; never write one inline.
	application.RegisterEvent[string](events.AppReady)
	application.RegisterEvent[services.CollectionInfo](events.CollectionOpened)
	application.RegisterEvent[services.Tree](events.CollectionChanged)
	application.RegisterEvent[git.State](events.GitChanged)
	// Void, not any: Wails validates an event's payload against its
	// registered type, and validating nil against an interface type
	// dereferences a nil reflect.Type. Void is the type for "no payload".
	application.RegisterEvent[application.Void](events.SettingsChanged)
	application.RegisterEvent[services.SendStarted](events.SendStarted)
	application.RegisterEvent[services.ResponseMeta](events.SendComplete)
	application.RegisterEvent[services.SendFailure](events.SendError)
	application.RegisterEvent[application.Void](events.SessionVarsChanged)
	application.RegisterEvent[services.Environments](events.EnvironmentsChanged)
}

func main() {
	// With arguments Otis is a CLI; with none it opens the desktop app.
	if len(os.Args) > 1 {
		cli.Version = services.Version
		os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
	}
	runGUI()
}

func runGUI() {
	settingsPath, err := settings.DefaultPath()
	if err != nil {
		log.Fatal(err)
	}
	// One store, shared: CollectionService writes the recents list that
	// SettingsService reads back.
	store := settings.NewStore(settingsPath)

	dialogs := services.NewDialogService()
	collections := services.NewCollectionService(store)

	// One secret store for the process, backed by the OS keychain. It is the
	// only place a real secret value is ever fetched; every read path the
	// window can reach uses secrets.Placeholder instead.
	//
	// A machine with no reachable keychain still gets a working app: the store
	// reports itself unavailable, the environment editor says so, and the
	// committed references are all still readable. Falling back to an
	// in-memory store would be worse than that — it would accept a secret and
	// silently forget it on quit.
	secretStore := secretStore()

	// The send service registers its own collection-close cleanup in
	// ServiceStartup — cookies, AWS credentials, held responses and session
	// variables all belong to the collection, not to the process.
	sends := services.NewSendService(collections, secretStore)

	app := application.New(application.Options{
		Name:        "Otis",
		Description: "File-based HTTP client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
			application.NewService(services.NewSettingsService(store)),
			application.NewService(dialogs),
			application.NewService(collections),
			application.NewService(services.NewGitService(collections)),
			application.NewService(services.NewRequestService(collections)),
			application.NewService(sends),
			application.NewService(services.NewEnvironmentService(collections, store, secretStore)),
			// The one service that writes to a repository, and only what a
			// review needs: the index, and a commit. internal/git stays
			// read-only.
			application.NewService(services.NewDiffService(collections)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	if runtime.GOOS == "darwin" {
		app.Menu.Set(macMenu(app, dialogs, collections))
	}

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Otis",
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  windowMinWidth,
		MinHeight: windowMinHeight,
		// A directory dropped anywhere on the shell opens as a collection;
		// the shell root carries the data-file-drop-target attribute.
		EnableFileDrop: true,
		Mac: application.MacWindow{
			// The traffic lights sit inset in our own title strip, which the
			// frontend draws inside the content area. The strip is made
			// draggable in CSS (--wails-draggable) rather than with
			// InvisibleTitleBarHeight, so the controls in it stay clickable.
			TitleBar: application.MacTitleBarHiddenInset,
			Backdrop: application.MacBackdropNormal,
		},
		// --bg (#09090b), so the window never flashes a different colour
		// before the frontend paints.
		BackgroundColour: application.NewRGB(9, 9, 11),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// secretStore is the process's secret store: the OS keychain, keyed as
// docs/FORMAT.md §5 describes, with its key index beside the settings file.
//
// A config directory that cannot be located costs the index, not the keychain:
// values are still stored and fetched, and only the editor's "which references
// have a value here" list goes empty. That is worth degrading rather than
// refusing to start.
func secretStore() secrets.Store {
	index, err := secrets.DefaultIndexPath()
	if err != nil {
		log.Printf("secrets: the key index is unavailable, so stored secrets cannot be listed: %v", err)
		index = ""
	}
	return secrets.NewKeyring(index)
}

// macMenu is the macOS application menu.
//
// macOS is the only platform where the menu bar is mandatory, and its default
// File menu binds Cmd+W to Close Window — which would swallow the Cmd+W the
// shell uses to close a document tab, since menu key equivalents are handled
// before the key reaches the webview. This menu therefore has no Close Window
// item: Otis is a single-window app, and Cmd+Q closes it.
//
// Windows and Linux keep Wails' default menu; neither binds Ctrl+W.
func macMenu(app *application.App, dialogs *services.DialogService, collections *services.CollectionService) *application.Menu {
	menu := application.NewMenu()
	menu.AddRole(application.AppMenu)

	file := menu.AddSubmenu("File")
	file.Add("Open Collection…").SetAccelerator("cmdorctrl+o").OnClick(func(*application.Context) {
		dir, err := dialogs.OpenDirectory()
		if err != nil || dir == "" {
			return
		}
		if _, err := collections.Open(dir); err != nil {
			app.Logger.Error("opening a collection from the menu", "path", dir, "error", err)
		}
	})

	menu.AddRole(application.EditMenu)
	menu.AddRole(application.ViewMenu)
	menu.AddRole(application.WindowMenu)
	return menu
}
