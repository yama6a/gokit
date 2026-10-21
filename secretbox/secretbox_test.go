package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKey(fill byte) Key {
	return Key(bytes.Repeat([]byte{fill}, keySize))
}

func TestParseKey(t *testing.T) {
	t.Parallel()

	enc := base64.StdEncoding.EncodeToString
	tests := []struct {
		name    string
		in      string
		want    Key
		wantErr error
	}{
		{name: "valid", in: enc(bytes.Repeat([]byte{7}, 32)), want: testKey(7)},
		{name: "too short", in: enc(make([]byte, 31)), wantErr: ErrMalformedKey},
		{name: "too long", in: enc(make([]byte, 33)), wantErr: ErrMalformedKey},
		{name: "empty", in: "", wantErr: ErrMalformedKey},
		{name: "not base64", in: "!!!not-base64!!!", wantErr: ErrMalformedKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseKey(tt.in)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBox_SealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plaintext []byte
		want      []byte
	}{
		{name: "text", plaintext: []byte("hunter2"), want: []byte("hunter2")},
		{name: "empty", plaintext: []byte{}, want: nil},
		{name: "binary", plaintext: []byte{0, 1, 2, 255, 254}, want: []byte{0, 1, 2, 255, 254}},
		{name: "large", plaintext: bytes.Repeat([]byte("x"), 64*1024), want: bytes.Repeat([]byte("x"), 64*1024)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			box, err := New(testKey(1))
			require.NoError(t, err)

			sealed, err := box.Seal(tt.plaintext)
			require.NoError(t, err)
			require.Len(t, sealed, 12+len(tt.plaintext)+16)

			got, err := box.Open(sealed)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBox_SealUsesFreshNonce(t *testing.T) {
	t.Parallel()

	box, err := New(testKey(1))
	require.NoError(t, err)

	a, err := box.Seal([]byte("same"))
	require.NoError(t, err)
	b, err := box.Seal([]byte("same"))
	require.NoError(t, err)

	require.NotEqual(t, a[:12], b[:12])
	require.NotEqual(t, a, b)
}

func TestBox_SealNonceReadFails(t *testing.T) {
	t.Parallel()

	box, err := New(testKey(1))
	require.NoError(t, err)
	errRead := errors.New("entropy exhausted")
	box.random = errReader{err: errRead}

	got, err := box.Seal([]byte("x"))
	require.ErrorIs(t, err, errRead)
	require.Nil(t, got)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestBox_OpenFailures(t *testing.T) {
	t.Parallel()

	box, err := New(testKey(1))
	require.NoError(t, err)
	other, err := New(testKey(2))
	require.NoError(t, err)
	sealed, err := box.Seal([]byte("secret"))
	require.NoError(t, err)

	flip := func(i int) []byte {
		c := bytes.Clone(sealed)
		c[i] ^= 1
		return c
	}
	tests := []struct {
		name string
		box  *Box
		in   []byte
	}{
		{name: "nil", box: box, in: nil},
		{name: "shorter than nonce", box: box, in: sealed[:5]},
		{name: "shorter than nonce plus tag", box: box, in: sealed[:20]},
		{name: "truncated ciphertext", box: box, in: sealed[:len(sealed)-1]},
		{name: "flipped nonce bit", box: box, in: flip(0)},
		{name: "flipped ciphertext bit", box: box, in: flip(13)},
		{name: "flipped tag bit", box: box, in: flip(len(sealed) - 1)},
		{name: "wrong key", box: other, in: sealed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.box.Open(tt.in)
			require.ErrorIs(t, err, ErrDecrypt)
			require.Nil(t, got)
		})
	}
}
