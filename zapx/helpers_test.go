package zapx_test

import (
	"encoding/json"
	"errors"

	"go.uber.org/zap/zapcore"
)

var errWrite = errors.New("write failed")

type errCore struct{}

func (errCore) Enabled(zapcore.Level) bool          { return true }
func (c errCore) With([]zapcore.Field) zapcore.Core { return c }
func (errCore) Check(zapcore.Entry, *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return nil
}
func (errCore) Write(zapcore.Entry, []zapcore.Field) error { return errWrite }
func (errCore) Sync() error                                { return nil }

func jsonNumber(s string) json.Number { return json.Number(s) }
