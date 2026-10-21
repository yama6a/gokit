package zapx

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const redactedValue = "[REDACTED]"

// DefaultRedactKeys returns the field keys that Redacting hides when it gets no keys.
func DefaultRedactKeys() []string {
	return []string{"token", "api_key", "secret", "password", "authorization", "cookie", "credentials"}
}

// Redacting wraps core so that fields with a matching key are logged as "[REDACTED]".
// Keys match case-insensitively, at the top level and inside objects, arrays, maps and namespaces.
// Wrap each core on its own: a tee writes to every inner core whatever its level.
func Redacting(core zapcore.Core, keys ...string) zapcore.Core {
	if len(keys) == 0 {
		keys = DefaultRedactKeys()
	}
	r := &redactor{keys: make(map[string]struct{}, len(keys))}
	for _, k := range keys {
		r.keys[strings.ToLower(k)] = struct{}{}
	}
	return &redactCore{Core: core, r: r}
}

type redactor struct {
	keys map[string]struct{}
}

func (r *redactor) matches(key string) bool {
	_, ok := r.keys[strings.ToLower(key)]
	return ok
}

// fields returns redacted copies of fields. all is true when a namespace that matched a key is open,
// which hides every later field. The returned bool reports the state after the last field.
func (r *redactor) fields(fields []zapcore.Field, all bool) ([]zapcore.Field, bool) {
	out := make([]zapcore.Field, len(fields))
	for i, f := range fields {
		switch {
		case f.Type == zapcore.NamespaceType:
			out[i] = f
			all = all || r.matches(f.Key)
		case f.Type == zapcore.InlineMarshalerType:
			out[i] = r.marshaler(f, all)
		case all || r.matches(f.Key):
			out[i] = zap.String(f.Key, redactedValue)
		default:
			out[i] = r.field(f)
		}
	}
	return out, all
}

func (r *redactor) field(f zapcore.Field) zapcore.Field {
	if f.Type == zapcore.ObjectMarshalerType {
		return r.marshaler(f, false)
	}
	if f.Type == zapcore.ArrayMarshalerType {
		m, ok := f.Interface.(zapcore.ArrayMarshaler)
		if !ok {
			return zap.String(f.Key, redactedValue)
		}
		f.Interface = redactArray{inner: m, r: r}
	}
	if f.Type == zapcore.ReflectType {
		f.Interface = r.reflected(f.Interface)
	}
	return f
}

// marshaler wraps an object or inline field. A field whose value is not an ObjectMarshaler is hidden.
func (r *redactor) marshaler(f zapcore.Field, all bool) zapcore.Field {
	m, ok := f.Interface.(zapcore.ObjectMarshaler)
	if !ok {
		return zap.String(f.Key, redactedValue)
	}
	f.Interface = redactObject{inner: m, r: r, all: all}
	return f
}

// reflected round-trips v through JSON, which is how zap encodes it, so that structs,
// typed maps and slices become plain maps and slices that walk can inspect.
// A value that does not survive the round trip is hidden entirely.
func (r *redactor) reflected(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return redactedValue
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return redactedValue
	}
	return r.walk(out)
}

func (r *redactor) walk(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if r.matches(k) {
				out[k] = redactedValue
				continue
			}
			out[k] = r.walk(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = r.walk(e)
		}
		return out
	default:
		return v
	}
}

type redactCore struct {
	zapcore.Core
	r   *redactor
	all bool
}

func (c *redactCore) With(fields []zapcore.Field) zapcore.Core {
	fs, all := c.r.fields(fields, c.all)
	return &redactCore{Core: c.Core.With(fs), r: c.r, all: all}
}

func (c *redactCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *redactCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	fs, _ := c.r.fields(fields, c.all)
	return c.Core.Write(ent, fs) //nolint:wrapcheck // decorator passes the wrapped error through
}

type redactObject struct {
	inner zapcore.ObjectMarshaler
	r     *redactor
	all   bool
}

func (o redactObject) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	return o.inner.MarshalLogObject(&redactEncoder{ObjectEncoder: enc, r: o.r, all: o.all}) //nolint:wrapcheck // decorator passes the wrapped error through
}

type redactArray struct {
	inner zapcore.ArrayMarshaler
	r     *redactor
}

func (a redactArray) MarshalLogArray(enc zapcore.ArrayEncoder) error {
	return a.inner.MarshalLogArray(redactArrayEncoder{ArrayEncoder: enc, r: a.r}) //nolint:wrapcheck // decorator passes the wrapped error through
}

// redactArrayEncoder only intercepts the appends that can carry a nested key.
type redactArrayEncoder struct {
	zapcore.ArrayEncoder
	r *redactor
}

func (e redactArrayEncoder) AppendArray(m zapcore.ArrayMarshaler) error {
	return e.ArrayEncoder.AppendArray(redactArray{inner: m, r: e.r}) //nolint:wrapcheck // decorator passes the wrapped error through
}

func (e redactArrayEncoder) AppendObject(m zapcore.ObjectMarshaler) error {
	return e.ArrayEncoder.AppendObject(redactObject{inner: m, r: e.r}) //nolint:wrapcheck // decorator passes the wrapped error through
}

func (e redactArrayEncoder) AppendReflected(v any) error {
	return e.ArrayEncoder.AppendReflected(e.r.reflected(v)) //nolint:wrapcheck // decorator passes the wrapped error through
}

// redactEncoder replaces the value of every key that matches. all is set once a matching
// namespace opens, because everything added after it lives inside that namespace.
type redactEncoder struct {
	zapcore.ObjectEncoder
	r   *redactor
	all bool
}

func (e *redactEncoder) hit(key string) bool {
	return e.all || e.r.matches(key)
}

func (e *redactEncoder) OpenNamespace(key string) {
	e.all = e.all || e.r.matches(key)
	e.ObjectEncoder.OpenNamespace(key)
}

func (e *redactEncoder) AddArray(key string, m zapcore.ArrayMarshaler) error {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return nil
	}
	return e.ObjectEncoder.AddArray(key, redactArray{inner: m, r: e.r}) //nolint:wrapcheck // decorator passes the wrapped error through
}

func (e *redactEncoder) AddObject(key string, m zapcore.ObjectMarshaler) error {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return nil
	}
	return e.ObjectEncoder.AddObject(key, redactObject{inner: m, r: e.r}) //nolint:wrapcheck // decorator passes the wrapped error through
}

func (e *redactEncoder) AddReflected(key string, v any) error {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return nil
	}
	return e.ObjectEncoder.AddReflected(key, e.r.reflected(v)) //nolint:wrapcheck // decorator passes the wrapped error through
}

func (e *redactEncoder) AddString(key, v string) {
	if e.hit(key) {
		v = redactedValue
	}
	e.ObjectEncoder.AddString(key, v)
}

func (e *redactEncoder) AddBinary(key string, v []byte) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddBinary(key, v)
}

func (e *redactEncoder) AddByteString(key string, v []byte) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddByteString(key, v)
}

func (e *redactEncoder) AddBool(key string, v bool) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddBool(key, v)
}

func (e *redactEncoder) AddComplex128(key string, v complex128) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddComplex128(key, v)
}

func (e *redactEncoder) AddComplex64(key string, v complex64) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddComplex64(key, v)
}

func (e *redactEncoder) AddDuration(key string, v time.Duration) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddDuration(key, v)
}

func (e *redactEncoder) AddFloat64(key string, v float64) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddFloat64(key, v)
}

func (e *redactEncoder) AddFloat32(key string, v float32) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddFloat32(key, v)
}

func (e *redactEncoder) AddInt(key string, v int) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddInt(key, v)
}

func (e *redactEncoder) AddInt64(key string, v int64) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddInt64(key, v)
}

func (e *redactEncoder) AddInt32(key string, v int32) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddInt32(key, v)
}

func (e *redactEncoder) AddInt16(key string, v int16) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddInt16(key, v)
}

func (e *redactEncoder) AddInt8(key string, v int8) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddInt8(key, v)
}

func (e *redactEncoder) AddTime(key string, v time.Time) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddTime(key, v)
}

func (e *redactEncoder) AddUint(key string, v uint) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddUint(key, v)
}

func (e *redactEncoder) AddUint64(key string, v uint64) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddUint64(key, v)
}

func (e *redactEncoder) AddUint32(key string, v uint32) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddUint32(key, v)
}

func (e *redactEncoder) AddUint16(key string, v uint16) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddUint16(key, v)
}

func (e *redactEncoder) AddUint8(key string, v uint8) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddUint8(key, v)
}

func (e *redactEncoder) AddUintptr(key string, v uintptr) {
	if e.hit(key) {
		e.ObjectEncoder.AddString(key, redactedValue)
		return
	}
	e.ObjectEncoder.AddUintptr(key, v)
}

var (
	_ zapcore.Core          = (*redactCore)(nil)
	_ zapcore.ObjectEncoder = (*redactEncoder)(nil)
	_ zapcore.ArrayEncoder  = redactArrayEncoder{}
)
