package app

import (
	goruntime "runtime"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// AboutInfo is what the About panel shows.
//
// It lives on the Go side so the version is the one actually compiled in
// rather than a number the frontend keeps its own copy of and forgets to
// update.
type AboutInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Developer   string `json:"developer"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Platform    string `json:"platform"`
	GoVersion   string `json:"goVersion"`
	LogDir      string `json:"logDir"`
}

// About returns the application and contact details.
func (a *App) About() AboutInfo {
	logDir, _ := LogDir()

	return AboutInfo{
		Name:        "PGSheet",
		Version:     a.version,
		Description: "Turns an Excel sheet into a reviewable PostgreSQL insert script.",
		Developer:   "Ali Zaarour",
		Email:       "zaarour.a@outlook.com",
		Phone:       "+96103979874",
		Platform:    goruntime.GOOS + "/" + goruntime.GOARCH,
		GoVersion:   goruntime.Version(),
		LogDir:      logDir,
	}
}

// Quit closes the application.
//
// It goes through the Wails runtime rather than os.Exit so the normal shutdown
// path runs: the operation in flight is cancelled, the pool is closed, and the
// log file is flushed (see Shutdown).
func (a *App) Quit() {
	if a.ctx == nil {
		return
	}
	runtime.Quit(a.ctx)
}
