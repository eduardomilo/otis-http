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

// OpenPostmanExport shows the native file picker for a Postman export.
//
// Cancelling returns "" with no error, the same as OpenDirectory: a person
// declining is not a failure.
func (s *DialogService) OpenPostmanExport() (string, error) {
	dialog := s.app.Dialog.OpenFile().
		SetTitle("Import from Postman").
		SetMessage("Choose a Postman collection export").
		SetButtonText("Choose").
		CanChooseDirectories(false).
		CanChooseFiles(true).
		ResolvesAliases(true).
		AddFilter("Postman export", "*.json")
	if w := s.app.Window.Current(); w != nil {
		dialog = dialog.AttachToWindow(w)
	}
	return dialog.PromptForSingleSelection()
}

// OpenImportDestination shows the directory picker for where an import
// should land.
func (s *DialogService) OpenImportDestination() (string, error) {
	dialog := s.app.Dialog.OpenFile().
		SetTitle("Import into").
		SetMessage("Choose where to write the imported collection").
		SetButtonText("Choose").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ResolvesAliases(true)
	if w := s.app.Window.Current(); w != nil {
		dialog = dialog.AttachToWindow(w)
	}
	return dialog.PromptForSingleSelection()
}

// OpenCollectionParent shows the directory picker for where a *new* collection
// should be created or cloned — the folder that will contain it, not the
// collection itself, which does not exist yet.
//
// One picker for both of screen 2b's remaining cards. They ask the same
// question and the name is typed in the dialog either way, so a second
// method here would differ only in its title.
func (s *DialogService) OpenCollectionParent() (string, error) {
	dialog := s.app.Dialog.OpenFile().
		SetTitle("Choose a location").
		SetMessage("Choose the folder to create the collection in").
		SetButtonText("Choose").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ResolvesAliases(true)
	if w := s.app.Window.Current(); w != nil {
		dialog = dialog.AttachToWindow(w)
	}
	return dialog.PromptForSingleSelection()
}
