package zapx_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yama6a/gokit/zapx"
)

const redacted = "[REDACTED]"

var typeNames = []string{
	"binary", "bytestring", "bool", "complex128", "complex64", "duration", "float64", "float32",
	"int", "int64", "int32", "int16", "int8", "time", "uint", "uint64", "uint32", "uint16", "uint8",
	"uintptr", "string", "array", "object", "reflected",
}

type allTypes struct {
	t      *testing.T
	prefix string
}

func (a allTypes) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	k := func(name string) string { return a.prefix + name }
	enc.AddBinary(k("binary"), []byte("v"))
	enc.AddByteString(k("bytestring"), []byte("v"))
	enc.AddBool(k("bool"), true)
	enc.AddComplex128(k("complex128"), 1)
	enc.AddComplex64(k("complex64"), 1)
	enc.AddDuration(k("duration"), time.Second)
	enc.AddFloat64(k("float64"), 1.5)
	enc.AddFloat32(k("float32"), 1.5)
	enc.AddInt(k("int"), 1)
	enc.AddInt64(k("int64"), 1)
	enc.AddInt32(k("int32"), 1)
	enc.AddInt16(k("int16"), 1)
	enc.AddInt8(k("int8"), 1)
	enc.AddTime(k("time"), time.Unix(0, 0))
	enc.AddUint(k("uint"), 1)
	enc.AddUint64(k("uint64"), 1)
	enc.AddUint32(k("uint32"), 1)
	enc.AddUint16(k("uint16"), 1)
	enc.AddUint8(k("uint8"), 1)
	enc.AddUintptr(k("uintptr"), 1)
	enc.AddString(k("string"), "v")
	require.NoError(a.t, enc.AddArray(k("array"), zapcore.ArrayMarshalerFunc(func(zapcore.ArrayEncoder) error { return nil })))
	require.NoError(a.t, enc.AddObject(k("object"), zapcore.ObjectMarshalerFunc(func(zapcore.ObjectEncoder) error { return nil })))
	require.NoError(a.t, enc.AddReflected(k("reflected"), map[string]string{"a": "b"}))
	return nil
}

type account struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Retries  int    `json:"retries"`
}

type nested struct{}

func (nested) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("user", "ann")
	enc.AddString("Token", "abc")
	enc.OpenNamespace("inner")
	enc.AddString("secret", "abc")
	enc.AddString("note", "ok")
	enc.OpenNamespace("credentials")
	enc.AddString("anything", "abc")
	return nil
}

func observed(t *testing.T, keys ...string) (zapcore.Core, *observer.ObservedLogs) {
	t.Helper()
	inner, logs := observer.New(zapcore.DebugLevel)
	return zapx.Redacting(inner, keys...), logs
}

func write(t *testing.T, core zapcore.Core, fields ...zapcore.Field) {
	t.Helper()
	zap.New(core).Info("msg", fields...)
}

func TestDefaultRedactKeys_Values(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"token", "api_key", "secret", "password", "authorization", "cookie", "credentials"},
		zapx.DefaultRedactKeys())
}

func TestRedacting_TopLevelFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []string
		in   zapcore.Field
		want zapcore.Field
	}{
		{"default key", nil, zap.String("token", "abc"), zap.String("token", redacted)},
		{"mixed case", nil, zap.String("Authorization", "Bearer x"), zap.String("Authorization", redacted)},
		{"non string", nil, zap.Int("password", 42), zap.String("password", redacted)},
		{"untouched", nil, zap.String("user", "ann"), zap.String("user", "ann")},
		{"substring is not a match", nil, zap.String("tokens", "abc"), zap.String("tokens", "abc")},
		{"custom key", []string{"ssn"}, zap.String("SSN", "123"), zap.String("SSN", redacted)},
		{"custom keys replace defaults", []string{"ssn"}, zap.String("token", "abc"), zap.String("token", "abc")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observed(t, tt.keys...)
			write(t, core, tt.in)
			require.Equal(t, []zapcore.Field{tt.want}, logs.All()[0].Context)
		})
	}
}

func TestRedacting_With(t *testing.T) {
	t.Parallel()

	core, logs := observed(t)
	l := zap.New(core).With(zap.String("api_key", "k"), zap.String("svc", "x"))
	l.Info("msg", zap.String("password", "p"))

	require.Equal(t, map[string]any{"api_key": redacted, "svc": "x", "password": redacted}, logs.All()[0].ContextMap())
}

func TestRedacting_WithDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	core, _ := observed(t)
	in := []zapcore.Field{zap.String("token", "abc")}
	core.With(in)
	require.Equal(t, []zapcore.Field{zap.String("token", "abc")}, in)
}

func TestRedacting_NestedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   zapcore.Field
		want map[string]any
	}{
		{
			"map of any",
			zap.Any("req", map[string]any{
				"user":    "ann",
				"headers": map[string]any{"Authorization": "Bearer x", "accept": "json"},
				"list":    []any{map[string]any{"cookie": "c"}, "plain", 3},
			}),
			map[string]any{"req": map[string]any{
				"user":    "ann",
				"headers": map[string]any{"Authorization": redacted, "accept": "json"},
				"list":    []any{map[string]any{"cookie": redacted}, "plain", jsonNumber("3")},
			}},
		},
		{
			"typed map",
			zap.Any("m", map[string]string{"Secret": "s", "a": "b"}),
			map[string]any{"m": map[string]any{"Secret": redacted, "a": "b"}},
		},
		{
			"struct with json tags",
			zap.Any("acct", account{Name: "ann", Password: "pw", Retries: 2}),
			map[string]any{"acct": map[string]any{"name": "ann", "password": redacted, "retries": jsonNumber("2")}},
		},
		{
			"slice of structs",
			zap.Any("accts", []account{{Name: "a", Password: "p"}}),
			map[string]any{"accts": []any{map[string]any{"name": "a", "password": redacted, "retries": jsonNumber("0")}}},
		},
		{
			"nil reflected",
			zap.Reflect("v", nil),
			map[string]any{"v": nil},
		},
		{
			"unmarshalable value is hidden",
			zap.Reflect("v", make(chan int)),
			map[string]any{"v": redacted},
		},
		{
			"object marshaler",
			zap.Object("o", nested{}),
			map[string]any{"o": map[string]any{
				"user":  "ann",
				"Token": redacted,
				"inner": map[string]any{
					"secret": redacted,
					"note":   "ok",
					"credentials": map[string]any{
						"anything": redacted,
					},
				},
			}},
		},
		{
			"dict",
			zap.Dict("d", zap.String("password", "p"), zap.String("a", "b")),
			map[string]any{"d": map[string]any{"password": redacted, "a": "b"}},
		},
		{
			"array of objects",
			zap.Objects("os", []zapcore.ObjectMarshaler{nested{}}),
			map[string]any{"os": []any{map[string]any{
				"user":  "ann",
				"Token": redacted,
				"inner": map[string]any{
					"secret": redacted,
					"note":   "ok",
					"credentials": map[string]any{
						"anything": redacted,
					},
				},
			}}},
		},
		{
			"array with reflected and nested array",
			zap.Array("a", zapcore.ArrayMarshalerFunc(func(enc zapcore.ArrayEncoder) error {
				require.NoError(t, enc.AppendReflected(map[string]string{"token": "t"}))
				return enc.AppendArray(zapcore.ArrayMarshalerFunc(func(enc zapcore.ArrayEncoder) error {
					return enc.AppendObject(zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
						enc.AddString("password", "p")
						return nil
					}))
				}))
			})),
			map[string]any{"a": []any{
				map[string]any{"token": redacted},
				[]any{map[string]any{"password": redacted}},
			}},
		},
		{
			"inline marshaler",
			zap.Inline(zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
				enc.AddString("password", "p")
				enc.AddString("a", "b")
				return nil
			})),
			map[string]any{"password": redacted, "a": "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observed(t)
			write(t, core, tt.in)
			require.Equal(t, tt.want, logs.All()[0].ContextMap())
		})
	}
}

func TestRedacting_Namespaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []zapcore.Field
		want   map[string]any
	}{
		{
			"key inside namespace",
			[]zapcore.Field{zap.String("top", "t"), zap.Namespace("req"), zap.String("Token", "abc"), zap.String("a", "b")},
			map[string]any{"top": "t", "req": map[string]any{"Token": redacted, "a": "b"}},
		},
		{
			"matching namespace hides all later fields",
			[]zapcore.Field{
				zap.Namespace("credentials"), zap.String("user", "ann"), zap.Int("n", 1),
				zap.Inline(zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
					enc.AddString("x", "y")
					return nil
				})),
			},
			map[string]any{"credentials": map[string]any{"user": redacted, "n": redacted, "x": redacted}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observed(t)
			write(t, core, tt.fields...)
			require.Equal(t, tt.want, logs.All()[0].ContextMap())
		})
	}
}

func TestRedacting_NamespaceOpenedInWith(t *testing.T) {
	t.Parallel()

	core, logs := observed(t)
	l := zap.New(core).With(zap.Namespace("secret"))
	l.Info("msg", zap.String("user", "ann"))

	require.Equal(t, map[string]any{"secret": map[string]any{"user": redacted}}, logs.All()[0].ContextMap())
}

func TestRedacting_EveryEncoderMethod(t *testing.T) {
	t.Parallel()

	keys := make([]string, 0, len(typeNames))
	hidden := make(map[string]any, len(typeNames))
	for _, n := range typeNames {
		keys = append(keys, "hide_"+n)
		hidden["hide_"+n] = redacted
	}
	core, logs := observed(t, keys...)
	write(t, core, zap.Object("h", allTypes{t: t, prefix: "hide_"}), zap.Object("s", allTypes{t: t, prefix: "show_"}))

	ctx := logs.All()[0].ContextMap()
	require.Equal(t, hidden, ctx["h"])
	shown := ctx["s"].(map[string]any)
	require.Len(t, shown, len(typeNames))
	require.Equal(t, "v", shown["show_string"])
	require.Equal(t, map[string]any{"a": "b"}, shown["show_reflected"])
	require.Equal(t, true, shown["show_bool"])
}

func TestRedacting_LevelFilter(t *testing.T) {
	t.Parallel()

	inner, logs := observer.New(zapcore.WarnLevel)
	l := zap.New(zapx.Redacting(inner))
	l.Info("dropped")
	l.Warn("kept", zap.String("token", "t"))

	require.Equal(t, 1, logs.Len())
	require.Equal(t, "kept", logs.All()[0].Message)
}

func TestRedacting_ReturnsInnerWriteError(t *testing.T) {
	t.Parallel()

	core := zapx.Redacting(errCore{})
	require.ErrorIs(t, core.Write(zapcore.Entry{}, nil), errWrite)
}

func TestRedacting_CheckSkipsDisabledLevel(t *testing.T) {
	t.Parallel()

	inner, _ := observer.New(zapcore.WarnLevel)
	core := zapx.Redacting(inner)

	require.Nil(t, core.Check(zapcore.Entry{Level: zapcore.InfoLevel}, nil))
}

func TestRedacting_MalformedFieldsAreHidden(t *testing.T) {
	t.Parallel()

	tests := []zapcore.Field{
		{Key: "a", Type: zapcore.ObjectMarshalerType, Interface: "not a marshaler"},
		{Key: "b", Type: zapcore.InlineMarshalerType, Interface: "not a marshaler"},
		{Key: "c", Type: zapcore.ArrayMarshalerType, Interface: "not a marshaler"},
	}
	for _, f := range tests {
		t.Run(f.Key, func(t *testing.T) {
			t.Parallel()
			core, logs := observed(t)
			write(t, core, f)
			require.Equal(t, []zapcore.Field{zap.String(f.Key, redacted)}, logs.All()[0].Context)
		})
	}
}
