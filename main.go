package main

import (
	"embed"
	"log"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	cli "github.com/otis-http/otis/cmd/otis"
	"github.com/otis-http/otis/internal/events"
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
	// Void, not any: Wails validates an event's payload against its
	// registered type, and validating nil against an interface type
	// dereferences a nil reflect.Type. Void is the type for "no payload".
	application.RegisterEvent[application.Void](events.SettingsChanged)
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

	app := application.New(application.Options{
		Name:        "Otis",
		Description: "File-based HTTP client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
			application.NewService(services.NewSettingsService(store)),
			application.NewService(dialogs),
			application.NewService(collections),
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
