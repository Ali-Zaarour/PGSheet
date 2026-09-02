package app

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"pgsheet/internal/validator"
)

// ProgressEvent is pushed to the frontend during long operations. The frontend
// subscribes with EventsOn("progress", ...).
type ProgressEvent struct {
	Phase   string `json:"phase"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Message string `json:"message"`
}

// progressInterval throttles emission. Wails event emission into the WebView is
// not free: flooding it freezes the UI, so at most ten events per second reach
// the frontend regardless of how fast chunks complete (spec §13).
const progressInterval = 100 * time.Millisecond

var (
	progressMu   sync.Mutex
	lastProgress time.Time
)

// emitProgress sends an event unless the previous one was too recent. A final
// event where current equals total is never dropped, so the UI cannot be left
// one chunk short of complete.
func (a *App) emitProgress(phase string, current, total int, message string) {
	if a.ctx == nil {
		return
	}

	final := total > 0 && current >= total

	progressMu.Lock()
	now := time.Now()
	if !final && now.Sub(lastProgress) < progressInterval {
		progressMu.Unlock()
		return
	}
	lastProgress = now
	progressMu.Unlock()

	runtime.EventsEmit(a.ctx, "progress", ProgressEvent{
		Phase:   phase,
		Current: current,
		Total:   total,
		Message: message,
	})
}

// progressFunc adapts the engine's progress callback to the throttled emitter.
func (a *App) progressFunc(defaultPhase string) validator.ProgressFunc {
	return func(phase string, current, total int) {
		if phase == "" {
			phase = defaultPhase
		}
		a.emitProgress(phase, current, total, "")
	}
}
