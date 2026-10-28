package backoff_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/gokit/internal/backoff"
)

func TestCeiling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    time.Duration
		limit   time.Duration
		attempt int
		want    time.Duration
	}{
		{name: "first attempt", base: time.Second, limit: 8 * time.Second, attempt: 1, want: time.Second},
		{name: "doubles", base: time.Second, limit: 8 * time.Second, attempt: 3, want: 4 * time.Second},
		{name: "capped", base: time.Second, limit: 8 * time.Second, attempt: 10, want: 8 * time.Second},
		{name: "no limit", base: time.Second, attempt: 5, want: 16 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, backoff.Ceiling(tt.base, tt.limit, tt.attempt))
		})
	}
}

func TestSleep(t *testing.T) {
	t.Parallel()

	t.Run("elapsed", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, backoff.Sleep(context.Background(), time.Millisecond))
	})

	t.Run("context ended", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, backoff.Sleep(ctx, time.Hour), context.Canceled)
	})
}
