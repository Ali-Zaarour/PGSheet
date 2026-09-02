// Package app is the only Wails-aware package. It owns the session and exposes
// it to the frontend; the engine packages it calls know nothing about Wails,
// which is what lets cmd/pgsheet-cli reuse them.
package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"pgsheet/internal/config"
	"pgsheet/internal/dbconn"
	"pgsheet/internal/domain"
	"pgsheet/internal/excel"
	"pgsheet/internal/introspect"
	"pgsheet/internal/validator"
)

// App is bound to the frontend: every exported method becomes a callable
// TypeScript function, so the exported surface is deliberate.
type App struct {
	version string
	log     *slog.Logger
	// closeLog releases the log file on shutdown.
	closeLog func()

	ctx context.Context

	// mu guards the session. The frontend can call bound methods concurrently
	// and long operations run on their own goroutines.
	mu sync.Mutex

	// cancel aborts the operation currently running, if any.
	cancel context.CancelFunc

	session session
}

// session is the whole of PGSheet's state: in memory for one run, never
// persisted.
type session struct {
	pool *dbconn.Pool
	// password is kept only to redact it out of error strings before they
	// cross to the frontend or reach a log.
	password string

	introspection *introspect.Result
	privileges    dbconn.Privileges

	workbook *excel.Workbook
	sheet    domain.SheetInfo

	mappings []domain.ColumnMapping
	pk       PKChoice

	settings   validator.Settings
	opts       validator.Options
	configName string

	// loadedConfig is kept so the header fingerprint can be compared when a
	// workbook is opened later. A configuration is often loaded first.
	loadedConfig *config.Config

	lastReport *validator.Report
	dryRunOK   bool
}

// New builds the application with the version stamped in at build time.
func New(version string) *App {
	logger, closeLog := setupLogging(version)

	return &App{
		version:  version,
		log:      logger,
		closeLog: closeLog,
		session: session{
			settings: validator.Settings{
				MaxIssues:               10000,
				ColumnMisalignThreshold: 0.30,
				CheckUniqueAgainstDB:    true,
				SkipBlankRows:           true,
			},
			opts: validator.Options{
				SourceTimezone: time.Local,
			},
			pk: PKChoice{Strategy: domain.PKNone},
		},
	}
}

// Startup receives the Wails context once the window exists.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.log.Info("pgsheet starting", "version", a.version)
}

// shutdownGrace bounds closing the pool. pgxpool.Close waits for every
// connection to come back, and one wedged in a network call must not keep the
// process alive after the window has gone.
const shutdownGrace = 3 * time.Second

// Shutdown releases everything held in memory, so credentials and sockets do
// not outlive the session.
func (a *App) Shutdown(context.Context) {
	a.mu.Lock()

	// Cancel first: an in-flight query holds a pooled connection, and the pool
	// cannot close until it is handed back.
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.closeSessionLocked()
	a.mu.Unlock()

	a.log.Info("pgsheet shutting down")
	if a.closeLog != nil {
		a.closeLog()
	}
}

// closeSessionLocked releases the pool and the workbook. The caller holds a.mu.
func (a *App) closeSessionLocked() {
	if a.session.pool != nil {
		pool := a.session.pool
		a.session.pool = nil

		done := make(chan struct{})
		go func() {
			pool.Close()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(shutdownGrace):
			// The connections will be reclaimed by the operating system when
			// the process ends. Say so rather than hanging silently.
			a.log.Warn("database connections did not close within the grace period",
				"grace", shutdownGrace.String())
		}
	}

	if a.session.workbook != nil {
		_ = a.session.workbook.Close()
		a.session.workbook = nil
	}

	// Dropping the reference is all Go allows: strings are immutable. It does
	// guarantee no later error or log line can read the password back.
	a.session.password = ""
	a.session.introspection = nil
	a.session.lastReport = nil
	a.session.dryRunOK = false
	a.session.loadedConfig = nil
}

// Version is the build version, shown in the UI and written into the header of
// every generated .sql file and configuration.
func (a *App) Version() string { return a.version }

// Cancel aborts the operation in progress. Workers observe it at chunk
// boundaries, and partial output files are removed by whoever created them.
func (a *App) Cancel() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	return nil
}

// operation sets up a cancellable context, so Cancel has something to cancel.
func (a *App) operation() (context.Context, func()) {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	a.cancel = cancel
	a.mu.Unlock()

	return ctx, func() {
		a.mu.Lock()
		if a.cancel != nil {
			a.cancel()
			a.cancel = nil
		}
		a.mu.Unlock()
	}
}

// redact removes the password before an error leaves the Go side. Connection
// failures are the ones most likely to be pasted into a ticket.
func (a *App) redact(err error) error {
	if err == nil {
		return nil
	}
	return dbconn.Redact(err, a.session.password)
}
