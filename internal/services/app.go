// Package services holds the Go services exposed to the frontend through
// Wails bindings. Each service is request/response only; anything that
// streams to the frontend goes over the Wails event system instead.
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Version is the build-time version string. It is overridden by the linker
// via -X github.com/otis-http/otis/internal/services.Version=<value>
// (see the BUILD_FLAGS in build/<platform>/Taskfile.yml).
var Version = "dev"

// EventAppReady is emitted once per window as soon as the Wails runtime in
// that window is ready. Its payload is the version string.
const EventAppReady = "app:ready"

// AppService is the root service of Otis. It carries app-level metadata and
// a health check used to prove the binding channel works.
type AppService struct {
	app *application.App
}

// NewAppService constructs an AppService. The *application.App is resolved
// lazily in ServiceStartup because the service is created before the app.
func NewAppService() *AppService {
	return &AppService{}
}

// ServiceStartup is called by Wails during application startup, before any
// event is dispatched, so hooks registered here cannot miss a window event.
func (s *AppService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	// Emit app:ready to a window as soon as its JS runtime has loaded.
	// Emitting at process start would race the webview and the event would
	// be dropped before any listener exists.
	for _, w := range s.app.Window.GetAll() {
		s.emitReadyWhenRuntimeLoads(w)
	}
	s.app.Window.OnCreate(s.emitReadyWhenRuntimeLoads)
	return nil
}

func (s *AppService) emitReadyWhenRuntimeLoads(w application.Window) {
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		s.app.Event.Emit(EventAppReady, Version)
	})
}

// Version returns the build-time version string.
func (s *AppService) Version() string {
	return Version
}

// Ping echoes msg back with a timestamp. It returns an error for an empty
// message so the error path of the binding channel is exercised too.
func (s *AppService) Ping(msg string) (string, error) {
	if msg == "" {
		return "", errors.New("ping: message must not be empty")
	}
	return fmt.Sprintf("pong: %s @ %s", msg, time.Now().Format(time.RFC3339)), nil
}
