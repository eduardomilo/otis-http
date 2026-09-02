package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	cli "github.com/otis-http/otis/cmd/otis"
	"github.com/otis-http/otis/internal/services"
)

// The production frontend build is embedded from frontend/dist.
// Do not change the Vite build.outDir; this path depends on it.
//
//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registering the event gives the generated TS bindings a typed name.
	application.RegisterEvent[string](services.EventAppReady)
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
	app := application.New(application.Options{
		Name:        "Otis",
		Description: "File-based HTTP client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Otis",
		Width:  1100,
		Height: 700,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(10, 10, 10),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
