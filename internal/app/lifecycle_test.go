package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pgsheet/internal/dbconn"
)

// deadPool builds a pool pointing at a port nothing listens on.
//
// pgxpool does not connect when it is created, so this needs no server: it is
// a real pool object whose close behaviour can be observed offline.
func deadPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	cfg, err := pgxpool.ParseConfig("postgres://nobody@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 200 * time.Millisecond

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

// acquireErr reports what the pool says when asked for a connection. A closed
// pool refuses immediately and says so, which is the observable difference
// between "closed" and "merely unreachable".
func acquireErr(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err == nil {
		conn.Release()
		return ""
	}
	return err.Error()
}

// Closing the window has to close the sockets. Nothing about this tool should
// keep a connection to a customer's database open after the operator is done
// with it (spec §5).
func TestShutdownClosesTheDatabaseConnection(t *testing.T) {
	a := New("test")
	pool := deadPool(t)
	a.session.pool = &dbconn.Pool{Pool: pool}
	a.session.password = "hunter2"

	a.Shutdown(context.Background())

	if err := acquireErr(t, pool); !strings.Contains(err, "closed") {
		t.Errorf("after shutdown the pool reports %q, want a closed pool", err)
	}
	if a.session.pool != nil {
		t.Error("the session still holds a pool after shutdown")
	}
	if a.session.password != "" {
		t.Error("the session still holds the password after shutdown")
	}
}

func TestDisconnectClosesTheDatabaseConnection(t *testing.T) {
	a := New("test")
	pool := deadPool(t)
	a.session.pool = &dbconn.Pool{Pool: pool}
	a.session.password = "hunter2"

	if err := a.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if err := acquireErr(t, pool); !strings.Contains(err, "closed") {
		t.Errorf("after disconnect the pool reports %q, want a closed pool", err)
	}
	if a.session.password != "" {
		t.Error("Disconnect left the password in the session")
	}
}

// Reconnecting must not leak the previous pool: a session that connects three
// times should hold one set of connections, not three.
func TestConnectingAgainClosesThePreviousPool(t *testing.T) {
	a := New("test")
	first := deadPool(t)
	a.session.pool = &dbconn.Pool{Pool: first}

	a.mu.Lock()
	a.closeSessionLocked()
	a.mu.Unlock()

	if err := acquireErr(t, first); !strings.Contains(err, "closed") {
		t.Errorf("the previous pool reports %q, want a closed pool", err)
	}
}

func TestShutdownIsSafeTwiceAndWithoutAConnection(t *testing.T) {
	a := New("test")

	// Never connected: shutting down must not panic on a nil pool.
	a.Shutdown(context.Background())

	pool := deadPool(t)
	a.session.pool = &dbconn.Pool{Pool: pool}
	a.Shutdown(context.Background())
	a.Shutdown(context.Background())
}

// Shutdown cancels the running operation before closing the pool, because the
// pool cannot close while a query still holds a connection.
func TestShutdownCancelsTheRunningOperation(t *testing.T) {
	a := New("test")

	ctx, done := a.operation()
	defer done()

	a.Shutdown(context.Background())

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("the operation context was not cancelled by shutdown")
	}
}

// A shutdown that cannot close the pool must still return, or the window
// disappears and the process stays alive.
func TestShutdownReturnsWithinTheGracePeriod(t *testing.T) {
	a := New("test")
	a.session.pool = &dbconn.Pool{Pool: deadPool(t)}

	finished := make(chan struct{})
	go func() {
		a.Shutdown(context.Background())
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Shutdown did not return; the process would stay alive after the window closed")
	}
}
