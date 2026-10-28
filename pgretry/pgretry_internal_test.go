package pgretry

import (
	"context"
	"database/sql/driver"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yama6a/gokit/internal/backoff"
)

var epoch = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

type safeToRetryError struct{}

func (safeToRetryError) Error() string     { return "nothing sent" }
func (safeToRetryError) SafeToRetry() bool { return true }

type fakeConn struct{ driver.Conn }

// fakeConnector fails its first len(errs) calls with errs in order, then succeeds.
type fakeConnector struct {
	driver.Connector
	errs  []error
	calls int
}

func (f *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return nil, f.errs[f.calls-1]
	}
	return fakeConn{}, nil
}

type clock struct {
	at    time.Time
	waits []time.Duration
}

func (c *clock) now() time.Time { return c.at }

func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.waits = append(c.waits, d)
	c.at = c.at.Add(d)
	return nil
}

func testConnector(inner driver.Connector) (*connector, *clock, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	clk := &clock{at: epoch}
	c := newConnector(inner, zap.New(core))
	c.now = clk.now
	c.sleep = clk.sleep
	c.jitter = func() float64 { return 0.5 }
	return c, clk, logs
}

// closedDSN returns a DSN for a local port that refuses connections.
func closedDSN(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return "postgres://app@" + addr + "/app?sslmode=disable&connect_timeout=1"
}

func refusedDialError(t *testing.T) error {
	t.Helper()
	cfg, err := pgx.ParseConfig(closedDSN(t))
	require.NoError(t, err)
	_, err = stdlib.GetConnector(*cfg).Connect(t.Context())
	var connectErr *pgconn.ConnectError
	require.ErrorAs(t, err, &connectErr)
	return err //nolint:wrapcheck // test helper
}

func TestConnect_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	dialErr := refusedDialError(t)
	tests := []struct {
		name string
		err  error
	}{
		{name: "connect error", err: dialErr},
		{name: "safe to retry", err: safeToRetryError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inner := &fakeConnector{errs: []error{tt.err, tt.err}}
			c, clk, logs := testConnector(inner)

			conn, err := c.Connect(t.Context())
			require.NoError(t, err)
			require.Equal(t, fakeConn{}, conn)
			require.Equal(t, 3, inner.calls)
			require.Equal(t, []time.Duration{125 * time.Millisecond, 250 * time.Millisecond}, clk.waits)
			require.Equal(t, 2, logs.FilterLevelExact(zapcore.WarnLevel).Len())
		})
	}
}

func TestConnect_GivesUpAfterBudget(t *testing.T) {
	t.Parallel()

	dialErr := refusedDialError(t)
	errs := make([]error, 100)
	for i := range errs {
		errs[i] = dialErr
	}
	inner := &fakeConnector{errs: errs}
	c, clk, logs := testConnector(inner)

	_, err := c.Connect(t.Context())
	require.ErrorIs(t, err, dialErr)
	require.LessOrEqual(t, clk.at.Sub(epoch), budget)
	require.Greater(t, clk.at.Sub(epoch), budget-maxWait)
	require.Equal(t, len(clk.waits)+1, inner.calls)
	require.Equal(t, len(clk.waits), logs.FilterLevelExact(zapcore.WarnLevel).Len())
}

func TestConnect_NoRetryAfterOtherErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "server error", err: &pgconn.PgError{Code: "42601"}},
		{name: "plain error", err: errors.New("tls handshake")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inner := &fakeConnector{errs: []error{tt.err}}
			c, clk, logs := testConnector(inner)

			_, err := c.Connect(t.Context())
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, 1, inner.calls)
			require.Empty(t, clk.waits)
			require.Zero(t, logs.Len())
		})
	}
}

func TestConnect_ContextEndsWait(t *testing.T) {
	t.Parallel()

	dialErr := refusedDialError(t)
	inner := &fakeConnector{errs: []error{dialErr, dialErr}}
	c, _, _ := testConnector(inner)
	ctx, cancel := context.WithCancel(t.Context())
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return backoff.Sleep(ctx, d)
	}

	_, err := c.Connect(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, dialErr)
	require.Equal(t, 1, inner.calls)
}

func TestOpen_InvalidDSN(t *testing.T) {
	t.Parallel()

	_, err := Open("postgres://app@127.0.0.1:notaport/app", zap.NewNop())
	require.Error(t, err)
}

func TestOpen_RetriesRefusedDial(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zapcore.WarnLevel)
	db, err := Open(closedDSN(t), zap.New(core))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.ErrorIs(t, db.PingContext(ctx), context.DeadlineExceeded)
	require.NotZero(t, logs.Len())
}
