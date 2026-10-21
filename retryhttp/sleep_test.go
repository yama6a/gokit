package retryhttp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSleep(t *testing.T) {
	t.Parallel()

	t.Run("elapsed", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, sleep(context.Background(), time.Millisecond))
	})

	t.Run("context ended", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, sleep(ctx, time.Hour), context.Canceled)
	})
}
