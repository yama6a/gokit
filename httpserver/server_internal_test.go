package httpserver

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type timeouts struct {
	readHeader, read, write, idle time.Duration
}

func timeoutsOf(srv *http.Server) timeouts {
	return timeouts{srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout}
}

func TestServer_Timeouts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts Options
		want timeouts
	}{
		{
			name: "defaults",
			want: timeouts{readHeader: 10 * time.Second},
		},
		{
			name: "all set",
			opts: Options{
				ReadHeaderTimeout: 2 * time.Second,
				ReadTimeout:       30 * time.Second,
				WriteTimeout:      time.Minute,
				IdleTimeout:       2 * time.Minute,
			},
			want: timeouts{readHeader: 2 * time.Second, read: 30 * time.Second, write: time.Minute, idle: 2 * time.Minute},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, timeoutsOf(New(tt.opts).httpServer()))
		})
	}
}
