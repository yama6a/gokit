package googleid

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"
)

const clientID = "client-1.apps.googleusercontent.com"

func sign(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	jws, err := signer.Sign(payload)
	require.NoError(t, err)
	token, err := jws.CompactSerialize()
	require.NoError(t, err)
	return token
}

func TestVerifier_Verify(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	v := newVerifier(&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}, clientID)

	claims := func(change func(map[string]any)) map[string]any {
		c := map[string]any{
			"iss":            "https://accounts.google.com",
			"aud":            clientID,
			"sub":            "1234567890",
			"email":          "Anna@Example.com",
			"email_verified": true,
			"name":           "Anna",
			"exp":            time.Now().Add(time.Hour).Unix(),
			"iat":            time.Now().Unix(),
		}
		if change != nil {
			change(c)
		}
		return c
	}
	want := Identity{Subject: "1234567890", Email: "anna@example.com", Name: "Anna"}

	tests := []struct {
		name  string
		token string
		ok    bool
	}{
		{"valid", sign(t, key, claims(nil)), true},
		{"issuer without scheme", sign(t, key, claims(func(c map[string]any) { c["iss"] = "accounts.google.com" })), true},
		{"other issuer", sign(t, key, claims(func(c map[string]any) { c["iss"] = "https://evil.example" })), false},
		{"other audience", sign(t, key, claims(func(c map[string]any) { c["aud"] = "other" })), false},
		{"expired", sign(t, key, claims(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() })), false},
		{"unverified email", sign(t, key, claims(func(c map[string]any) { c["email_verified"] = false })), false},
		{"no email", sign(t, key, claims(func(c map[string]any) { delete(c, "email") })), false},
		{"other key", sign(t, other, claims(nil)), false},
		{"garbage", "not-a-token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := v.Verify(t.Context(), tt.token)
			if !tt.ok {
				require.ErrorIs(t, err, ErrInvalidToken)
				return
			}
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}
