package zapx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/yama6a/gokit/zapx"
)

type fakeSink struct {
	mu       sync.Mutex
	entries  []zapx.Entry
	err      error
	block    bool
	deadline bool
}

func (s *fakeSink) Write(ctx context.Context, e zapx.Entry) error {
	if s.block {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.deadline = errors.Is(ctx.Err(), context.DeadlineExceeded)
		return fmt.Errorf("sink: %w", ctx.Err())
	}
	_, s.deadline = ctx.Deadline()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return s.err
}

func (s *fakeSink) all() []zapx.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]zapx.Entry(nil), s.entries...)
}

func TestSinkCore_WritesEntry(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	l := zap.New(zapx.SinkCore(sink, zapcore.InfoLevel), zap.AddCaller()).Named("api").With(zap.String("svc", "x"))
	l.Warn("hello", zap.Int("n", 1), zap.Namespace("req"), zap.String("id", "7"))

	got := sink.all()
	require.Len(t, got, 1)
	require.NotEmpty(t, got[0].Caller)
	require.False(t, got[0].At.IsZero())
	got[0].At = time.Time{}
	got[0].Caller = ""
	require.Equal(t, zapx.Entry{
		Level:   zapcore.WarnLevel,
		Logger:  "api",
		Message: "hello",
		Fields:  map[string]any{"svc": "x", "n": int64(1), "req": map[string]any{"id": "7"}},
	}, got[0])
	require.True(t, sink.deadline)
}

func TestSinkCore_NoCallerWithoutAddCaller(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	zap.New(zapx.SinkCore(sink, zapcore.InfoLevel)).Info("m")

	require.Empty(t, sink.all()[0].Caller)
}

func TestSinkCore_RespectsLevel(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	l := zap.New(zapx.SinkCore(sink, zapcore.WarnLevel))
	l.Info("dropped")
	l.Error("kept")

	got := sink.all()
	require.Len(t, got, 1)
	require.Equal(t, "kept", got[0].Message)
}

func TestSinkCore_WithDoesNotShareState(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	base := zap.New(zapx.SinkCore(sink, zapcore.InfoLevel)).With(zap.String("a", "1"))
	base.With(zap.String("b", "2")).Info("one")
	base.With(zap.String("c", "3")).Info("two")

	got := sink.all()
	require.Equal(t, map[string]any{"a": "1", "b": "2"}, got[0].Fields)
	require.Equal(t, map[string]any{"a": "1", "c": "3"}, got[1].Fields)
}

func TestSinkCore_DropsSinkError(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{err: errors.New("db down")}
	core := zapx.SinkCore(sink, zapcore.InfoLevel)

	require.NoError(t, core.Write(zapcore.Entry{Message: "m"}, nil))
	require.NoError(t, core.Sync())
	require.Len(t, sink.all(), 1)
}

func TestSinkCore_OnError(t *testing.T) {
	t.Parallel()

	boom := errors.New("db down")
	var got []error
	sink := &fakeSink{err: boom}
	core := zapx.SinkCore(sink, zapcore.InfoLevel, zapx.OnError(func(err error) { got = append(got, err) }))

	require.NoError(t, core.Write(zapcore.Entry{}, nil))
	require.Equal(t, []error{boom}, got)
}

func TestSinkCore_Timeout(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{block: true}
	var got []error
	core := zapx.SinkCore(sink, zapcore.InfoLevel,
		zapx.WithTimeout(10*time.Millisecond),
		zapx.OnError(func(err error) { got = append(got, err) }))

	require.NoError(t, core.Write(zapcore.Entry{}, nil))
	require.Len(t, got, 1)
	require.ErrorIs(t, got[0], context.DeadlineExceeded)
	require.True(t, sink.deadline)
}

type syncBuffer struct {
	bytes.Buffer
}

func (*syncBuffer) Sync() error { return nil }

func TestNew_WritesJSONAndMirrorsToSinks(t *testing.T) {
	t.Parallel()

	var out syncBuffer
	sink := &fakeSink{}
	l := zapx.New(zapx.Options{Level: zapcore.InfoLevel, Sinks: []zapx.Sink{sink}, Output: &out})
	l.Debug("dropped")
	l.With(zap.String("api_key", "k")).Info("hello", zap.String("Password", "p"), zap.String("user", "ann"))

	var line map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &line))
	_, err := time.Parse("2006-01-02T15:04:05.000Z0700", line["ts"].(string))
	require.NoError(t, err)
	require.Equal(t, "info", line["level"])
	require.Equal(t, "hello", line["msg"])
	require.Equal(t, redacted, line["api_key"])
	require.Equal(t, redacted, line["Password"])
	require.Equal(t, "ann", line["user"])
	require.NotEmpty(t, line["caller"])

	got := sink.all()
	require.Len(t, got, 1)
	require.Equal(t, map[string]any{"api_key": redacted, "Password": redacted, "user": "ann"}, got[0].Fields)
}

func TestNew_CustomRedactKeys(t *testing.T) {
	t.Parallel()

	var out syncBuffer
	sink := &fakeSink{}
	l := zapx.New(zapx.Options{Sinks: []zapx.Sink{sink}, Output: &out, RedactKeys: []string{"ssn"}})
	l.Info("m", zap.String("ssn", "1"), zap.String("token", "t"))

	require.Equal(t, map[string]any{"ssn": redacted, "token": "t"}, sink.all()[0].Fields)
}

func TestNew_StacktraceOnError(t *testing.T) {
	t.Parallel()

	var out syncBuffer
	zapx.New(zapx.Options{Output: &out}).Error("bad")

	var line map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &line))
	require.NotEmpty(t, line["stacktrace"])
}

func TestNew_DefaultsToStdout(t *testing.T) {
	t.Parallel()

	l := zapx.New(zapx.Options{Level: zapcore.FatalLevel})
	require.False(t, l.Core().Enabled(zapcore.InfoLevel))
}

func TestSinkCore_CheckSkipsDisabledLevel(t *testing.T) {
	t.Parallel()

	core := zapx.SinkCore(&fakeSink{}, zapcore.WarnLevel)

	require.Nil(t, core.Check(zapcore.Entry{Level: zapcore.InfoLevel}, nil))
}
