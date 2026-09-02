package services

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// DialogService exposes the native file dialogs. The frontend never touches
// the filesystem itself; picking a directory is a Go call that returns a path.
type DialogService struct {
	app *application.App
}

// NewDialogService constructs a DialogService.
func NewDialogService() *DialogService { return &DialogService{} }

// ServiceStartup resolves the running application.
func (s *DialogService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	return nil
}

// OpenDirectory shows the native directory picker and returns the chosen
// absolute path. Cancelling returns "" with no error: the user declining is
// not a failure, and every caller has to handle the empty string anyway.
func (s *DialogService) OpenDirectory() (string, error) {
	dialog := s.app.Dialog.OpenFile().
		SetTitle("Open collection").
		SetMessage("Choose a folder of .http files").
		SetButtonText("Open").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ResolvesAliases(true)
	if w := s.app.Window.Current(); w != nil {
		dialog = dialog.AttachToWindow(w)
	}
	return dialog.PromptForSingleSelection()
}
