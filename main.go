package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

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
