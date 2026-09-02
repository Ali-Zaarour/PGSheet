package app

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Native dialogs, not HTML file inputs.
//
// A browser file input hands back a File object with no filesystem path, which
// is useless here: the engine streams a workbook from disk and writes a .sql
// file to a path the operator chose. The native dialog is the only thing that
// gives a real path (spec §15).

func (a *App) openFileDialog(title, filterLabel, pattern string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
		Filters: []runtime.FileFilter{
			{DisplayName: filterLabel, Pattern: pattern},
		},
		ShowHiddenFiles:            false,
		TreatPackagesAsDirectories: false,
	})
}

func (a *App) saveFileDialog(title, defaultName, filterLabel, pattern string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                title,
		DefaultFilename:      defaultName,
		CanCreateDirectories: true,
		Filters: []runtime.FileFilter{
			{DisplayName: filterLabel, Pattern: pattern},
		},
	})
}
