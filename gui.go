//go:build !otis_cli

package main

import (
	"embed"
	"errors"
	"io/fs"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailsevents "github.com/wailsapp/wails/v3/pkg/events"

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

// appIdentifier is Otis' identity to the operating system: the macOS bundle
// identifier (build/config.yml `info.productIdentifier`, from which
// build/darwin/Info.plist is generated) and the single-instance lock's unique
// ID, which Linux turns into the D-Bus name org.<id>.SingleInstance.
//
// The two are deliberately the same string. They are both "which app is
// this", and a second answer to that is a second app as far as the OS is
// concerned: the lock would not stop a second launch of the bundle, and the
// file association would point somewhere the lock does not cover.
//
// Reverse-DNS of the GitHub organisation rather than of a domain, because
// that is a namespace the project verifiably controls. It must not change
// once there are installs: macOS keys the .http file association and the
// user's default-application choice on it, so a change reads as a different
// app and leaves the old registration behind. Nothing of the user's is keyed
// on it — settings.json and the key index live under os.UserConfigDir()/otis,
// and keychain entries under <collection>/<env>/<name> — so a change costs a
// re-registration and nothing else.
const appIdentifier = "io.github.otis-http.otis"

// requestExtension is the file type Otis registers. Wails matches argv[1]
// against this list on Windows and Linux before raising
// events.Common.ApplicationOpenedWithFile; macOS goes through the
// NSApplication delegate and CFBundleDocumentTypes in Info.plist instead.
// build/config.yml `fileAssociations` is the other half, and generates both.
var fileAssociations = []string{".http"}

func init() {
	// Registering each event gives the generated TS bindings its payload
	// type. The names come from internal/events; never write one inline.
	application.RegisterEvent[string](events.AppReady)
	application.RegisterEvent[services.CollectionInfo](events.CollectionOpened)
	application.RegisterEvent[application.Void](events.OpenNode)
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
	application.RegisterEvent[services.ScriptTest](events.ScriptTest)
	application.RegisterEvent[services.ScriptConsole](events.ScriptConsole)
	application.RegisterEvent[services.RunStarted](events.RunStarted)
	application.RegisterEvent[services.RunResult](events.RunResult)
	application.RegisterEvent[services.RunComplete](events.RunComplete)
	application.RegisterEvent[services.MCPConfirmation](events.MCPConfirm)
	application.RegisterEvent[services.MCPResolved](events.MCPConfirmResolved)
	application.RegisterEvent[services.MCPStatus](events.MCPChanged)
}

// runGUI opens the window. openPath is a file or directory to show, or "" to
// open on whatever collection was last used.
func runGUI(openPath string) {
	// A binary with no frontend bundle is a working CLI and cannot be the
	// app. Saying so beats opening a blank window.
	if err := assetsPresent(); err != nil {
		log.Fatalf("otis: %v", err)
	}

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

	// The services the MCP server delegates to. It is constructed with them
	// rather than reimplementing anything, which is what makes an agent's
	// write subject to the invariants a person's is (docs/MCP.md §12). Note
	// that it gets the *services*, never the secret store: no tool reads or
	// writes a secret, and the way that is guaranteed is that this service
	// has no way to.
	gitState := services.NewGitService(collections)
	requestSvc := services.NewRequestService(collections)
	environmentSvc := services.NewEnvironmentService(collections, store, secretStore)
	folderSvc := services.NewFolderService(collections, sends)
	agents := services.NewMCPService(
		store, collections, sends, requestSvc, folderSvc, environmentSvc, gitState)

	app := application.New(application.Options{
		Name:        "Otis",
		Description: "File-based HTTP client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
			application.NewService(services.NewSettingsService(store)),
			application.NewService(dialogs),
			application.NewService(collections),
			application.NewService(gitState),
			application.NewService(requestSvc),
			application.NewService(sends),
			application.NewService(environmentSvc),
			// The one service that writes to a repository, and only what a
			// review needs: the index, and a commit. internal/git stays
			// read-only.
			application.NewService(services.NewDiffService(collections)),
			// The folder view borrows the sender's session store: the
			// variables a run set are half of its Variables panel, and they
			// live nowhere on disk (docs/FORMAT.md §4.5).
			application.NewService(folderSvc),
			application.NewService(services.NewOrderService(collections)),
			// The agent server. It starts nothing on its own: the listener
			// and each capability are separate switches and all of them are
			// off in the zero settings, so a fresh install exposes nothing.
			application.NewService(agents),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// The .http file type. On Windows and Linux this is what makes Wails
		// recognise argv[1] as a file to open rather than ignore it; on macOS
		// the Info.plist declaration does the work and this is unused.
		FileAssociations: fileAssociations,
		// One Otis per machine. A second launch hands its arguments to the
		// first and exits, which is what makes the file association behave:
		// double-clicking a second .http file navigates the window you have
		// instead of opening a window that fights it over the same collection,
		// the same watcher and the same keychain prompts.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               appIdentifier,
			OnSecondInstanceLaunch: forwardSecondInstance(collections),
		},
	})

	if runtime.GOOS == "darwin" {
		app.Menu.Set(macMenu(app, dialogs, collections))
	}

	// A .http file opened from Finder, Explorer or a file manager. On macOS
	// this is the NSApplication delegate and fires for every open, launch or
	// not; on Windows and Linux it is Wails reading argv[1] at startup, which
	// main() has already turned into openPath — so the two paths converge on
	// CollectionService.OpenPath and only one of them is ever the one that
	// fires.
	app.Event.OnApplicationEvent(wailsevents.Common.ApplicationOpenedWithFile, func(e *application.ApplicationEvent) {
		name := e.Context().Filename()
		if name == "" {
			return
		}
		if err := collections.OpenPath(name); err != nil {
			app.Logger.Error("opening a file from the desktop", "path", name, "error", err)
		}
	})

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

	// A path from the command line or a file association. The collection
	// opens now and the window is told which node to show as soon as its
	// runtime is ready — CollectionService.OpenPath holds the target until
	// then, because at this point the webview does not exist.
	if openPath != "" {
		if err := collections.OpenPath(openPath); err != nil {
			log.Printf("otis: %v", err)
		}
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// forwardSecondInstance handles a second launch: its arguments arrive here
// instead of starting another Otis.
//
// Only a path is acted on, decided by the same rule main() uses, so a second
// process that was really a command line (`otis ls` while the window is open)
// is not silently turned into a navigation. That case does not reach here at
// all — the CLI answers before application.New acquires the lock — but the
// rule is applied again rather than assumed, since this callback is the one
// place arguments arrive from a process whose dispatch we did not see.
//
// A relative path is resolved against the *second* process's working
// directory, which is why this uses WindowPathIn and not WindowPath:
// `otis ./create-order.http` typed in another terminal means the file beside
// that terminal, and this process's own directory is wherever it was started
// — "/" for an app launched from Finder. Getting that wrong fails silently,
// because a path that does not exist is not treated as a path at all: the
// second launch would just focus the window and drop the file.
func forwardSecondInstance(collections *services.CollectionService) func(application.SecondInstanceData) {
	return func(data application.SecondInstanceData) {
		args := data.Args
		if len(args) > 0 {
			args = args[1:] // drop argv[0]
		}
		path, ok := cli.WindowPathIn(data.WorkingDir, args)
		if !ok {
			// No path: the user launched Otis again to get at the window they
			// already have. Bringing it to the front is the whole answer.
			focusWindow()
			return
		}
		if err := collections.OpenPath(path); err != nil {
			log.Printf("otis: %v", err)
		}
		focusWindow()
	}
}

// focusWindow brings the window to the front, which is what a second launch
// is asking for however it was started.
//
// Show before Focus: Show is makeKeyAndOrderFront on macOS and the equivalent
// elsewhere, and it also un-hides a window that was hidden, which Focus alone
// does not.
//
// A known limit on macOS: this raises the window within Otis but does not
// raise Otis above another application, because that needs
// -[NSApplication activateIgnoringOtherApps:] and Wails v3 beta.16 exposes no
// cross-application activate. It costs nothing in the cases a user actually
// meets — double-clicking the app, a .http file, or the Dock icon all go
// through LaunchServices, which activates Otis itself and never reaches this
// function. What is left is `otis file.http` typed in a terminal while the app
// is already open: the file opens in the window, and the window has to be
// switched to by hand. docs/BUILDING.md records it.
func focusWindow() {
	app := application.Get()
	if app == nil {
		return
	}
	w := app.Window.Current()
	if w == nil {
		for _, candidate := range app.Window.GetAll() {
			w = candidate
			break
		}
	}
	if w == nil {
		return
	}
	w.Show()
	w.Focus()
}

// assetsPresent reports whether the embedded frontend bundle is real.
//
// frontend/dist is generated and git-ignored, so a module fetched from the
// proxy — `go install github.com/otis-http/otis@latest` — carries only the
// .gitkeep that keeps the //go:embed pattern matching at all (see
// .gitignore). The binary that comes out is a perfectly good `otis` command
// and cannot be the desktop app, so it says which. Without this check the
// window opens and renders nothing, which looks like a bug in Otis rather
// than a consequence of how it was built.
//
// The documented way to install just the command line is
// `go install -tags otis_cli`, which leaves this file out of the build
// entirely and needs no cgo and no platform toolkit. docs/BUILDING.md says so.
func assetsPresent() error {
	if _, err := fs.Stat(assets, "frontend/dist/index.html"); err != nil {
		return errors.New("this build has no frontend bundle, so it has no window.\n" +
			"       The command line works — try `otis --help`.\n" +
			"       For the desktop app, install a release:\n" +
			"       https://github.com/otis-http/otis/releases")
	}
	return nil
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
