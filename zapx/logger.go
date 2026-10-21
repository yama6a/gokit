package zapx

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Options configures New.
type Options struct {
	Level      zapcore.Level
	Sinks      []Sink
	Output     zapcore.WriteSyncer
	RedactKeys []string
}

// New returns a logger that writes JSON to o.Output and mirrors entries into every sink.
// Output defaults to stdout. RedactKeys defaults to DefaultRedactKeys.
func New(o Options) *zap.Logger {
	out := o.Output
	if out == nil {
		out = zapcore.Lock(os.Stdout)
	}
	cfg := zap.NewProductionEncoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder

	cores := make([]zapcore.Core, 0, 1+len(o.Sinks))
	cores = append(cores, Redacting(zapcore.NewCore(zapcore.NewJSONEncoder(cfg), out, o.Level), o.RedactKeys...))
	for _, s := range o.Sinks {
		cores = append(cores, Redacting(SinkCore(s, o.Level), o.RedactKeys...))
	}
	return zap.New(zapcore.NewTee(cores...), zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}
