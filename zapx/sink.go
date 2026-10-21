package zapx

import (
	"context"
	"slices"
	"time"

	"go.uber.org/zap/zapcore"
)

const defaultSinkTimeout = 5 * time.Second

// Entry is a log record as a Sink receives it.
type Entry struct {
	At      time.Time
	Level   zapcore.Level
	Logger  string
	Message string
	Caller  string
	Fields  map[string]any
}

// SinkOption configures a core built by SinkCore.
type SinkOption func(*sinkCore)

// WithTimeout sets how long one Sink.Write may take. The default is 5 seconds.
func WithTimeout(d time.Duration) SinkOption {
	return func(c *sinkCore) { c.timeout = d }
}

// OnError sets the function that receives errors from Sink.Write. Without it the core drops them.
func OnError(fn func(error)) SinkOption {
	return func(c *sinkCore) { c.onError = fn }
}

// SinkCore returns a core that writes every entry at or above level to sink.
// It never returns a sink error to zap.
func SinkCore(sink Sink, level zapcore.LevelEnabler, opts ...SinkOption) zapcore.Core {
	c := &sinkCore{LevelEnabler: level, sink: sink, timeout: defaultSinkTimeout}
	for _, o := range opts {
		o(c)
	}
	return c
}

type sinkCore struct {
	zapcore.LevelEnabler
	sink    Sink
	timeout time.Duration
	onError func(error)
	context []zapcore.Field
}

func (c *sinkCore) With(fields []zapcore.Field) zapcore.Core {
	clone := *c
	clone.context = slices.Concat(c.context, fields)
	return &clone
}

func (c *sinkCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *sinkCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.context {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}
	e := Entry{
		At:      ent.Time,
		Level:   ent.Level,
		Logger:  ent.LoggerName,
		Message: ent.Message,
		Fields:  enc.Fields,
	}
	if ent.Caller.Defined {
		e.Caller = ent.Caller.TrimmedPath()
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	if err := c.sink.Write(ctx, e); err != nil && c.onError != nil {
		c.onError(err)
	}
	return nil
}

func (c *sinkCore) Sync() error { return nil }

var _ zapcore.Core = (*sinkCore)(nil)
