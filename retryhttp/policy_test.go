package retryhttp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/gokit/retryhttp"
)

func TestPolicyFrom(t *testing.T) {
	t.Parallel()

	t.Run("absent", func(t *testing.T) {
		t.Parallel()

		got, ok := retryhttp.PolicyFrom(context.Background())
		require.False(t, ok)
		require.Equal(t, retryhttp.Policy{}, got)
	})

	t.Run("present", func(t *testing.T) {
		t.Parallel()

		want := retryhttp.Policy{Attempts: 4, Base: 10, Max: 100}
		got, ok := retryhttp.PolicyFrom(retryhttp.WithPolicy(context.Background(), want))
		require.True(t, ok)
		require.Equal(t, want, got)
	})
}

func TestDefaultRetryOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp *http.Response
		err  error
		want bool
	}{
		{name: "200", resp: &http.Response{StatusCode: http.StatusOK}, want: false},
		{name: "301", resp: &http.Response{StatusCode: http.StatusMovedPermanently}, want: false},
		{name: "400", resp: &http.Response{StatusCode: http.StatusBadRequest}, want: false},
		{name: "404", resp: &http.Response{StatusCode: http.StatusNotFound}, want: false},
		{name: "408", resp: &http.Response{StatusCode: http.StatusRequestTimeout}, want: false},
		{name: "429", resp: &http.Response{StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "500", resp: &http.Response{StatusCode: http.StatusInternalServerError}, want: true},
		{name: "503", resp: &http.Response{StatusCode: http.StatusServiceUnavailable}, want: true},
		{name: "connection error", err: errors.New("connection reset"), want: true},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "wrapped canceled", err: fmt.Errorf("do: %w", context.Canceled), want: false},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, retryhttp.DefaultRetryOn(tt.resp, tt.err))
		})
	}
}
