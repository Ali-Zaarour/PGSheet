// Command pgsheet is the Wails desktop shell for PGSheet.
//
// This file and internal/app are the only Wails-aware code in the project.
// Everything the application actually does lives in the engine packages under
// internal/, which have no knowledge of the UI shell — see CLAUDE.md.
package main

import (
	"embed"
	"log"

	"pgsheet/internal/app"
	"pgsheet/internal/version"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a := app.New(version.String())

	err := wails.Run(&options.App{
		Title:     "PGSheet",
		Width:     1280,
		Height:    860,
		MinWidth:  1024,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  a.Startup,
		OnShutdown: a.Shutdown,
		Bind: []any{
			a,
		},
		Windows: &windows.Options{
			// The runtime is never downloaded at run time: an offline machine
			// must fail loudly with an actionable message instead of hanging on
			// a network call. Installation supplies the runtime (spec §2).
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		log.Fatalf("pgsheet: %v", err)
	}
}
