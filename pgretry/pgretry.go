// Package pgretry opens a database/sql handle on the pgx driver that retries a failed dial.
// database/sql already retries a stale pooled connection, but not a new connection whose dial
// fails, such as while a CloudNativePG switchover leaves the -rw Service without a primary.
package pgretry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"

	"github.com/yama6a/gokit/internal/backoff"
)

const (
	budget   = 15 * time.Second
	baseWait = 250 * time.Millisecond
	maxWait  = 2 * time.Second
)

// Open returns a *sql.DB on the pgx stdlib driver for dsn.
// A new connection retries a failed dial for up to 15 seconds, or until its context ends,
// and logs each failure at WARN. It never retries once a statement may have reached the server.
func Open(dsn string, log *zap.Logger) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	return sql.OpenDB(newConnector(stdlib.GetConnector(*cfg), log)), nil
}

type connector struct {
	driver.Connector
	log    *zap.Logger
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	jitter func() float64
}

func newConnector(inner driver.Connector, log *zap.Logger) *connector {
	return &connector{Connector: inner, log: log, now: time.Now, sleep: backoff.Sleep, jitter: rand.Float64}
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	deadline := c.now().Add(budget)
	for attempt := 1; ; attempt++ {
		conn, err := c.Connector.Connect(ctx)
		if err == nil || ctx.Err() != nil || !dialFailed(err) {
			return conn, err //nolint:wrapcheck // database/sql inspects driver errors, so they pass through unchanged
		}
		wait := time.Duration(c.jitter() * float64(backoff.Ceiling(baseWait, maxWait, attempt)))
		if c.now().Add(wait).After(deadline) {
			return nil, err //nolint:wrapcheck // database/sql inspects driver errors, so they pass through unchanged
		}
		c.log.Warn("postgres dial failed, retrying",
			zap.Int("attempt", attempt), zap.Duration("wait", wait), zap.Error(err))
		if serr := c.sleep(ctx, wait); serr != nil {
			return nil, fmt.Errorf("wait before dial retry: %w: %w", serr, err)
		}
	}
}

func dialFailed(err error) bool {
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr) || pgconn.SafeToRetry(err)
}
