// Package googleid verifies Google ID tokens, such as the one the central SSO stores in a cookie.
package googleid

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	certsURL = "https://www.googleapis.com/oauth2/v3/certs"
	// issuer also accepts "accounts.google.com": go-oidc allows that form for Google only.
	issuer = "https://accounts.google.com"
)

// Identity is the verified Google account behind an ID token.
type Identity struct {
	Subject string
	Email   string // lowercase
	Name    string
}

// Verifier checks the signature, issuer, audience and expiry of Google ID tokens.
type Verifier struct {
	v *oidc.IDTokenVerifier
}

// New returns a Verifier for tokens issued to clientID. It fetches keys with the HTTP client of
// oidc.ClientContext in ctx. ctx must live as long as the Verifier.
func New(ctx context.Context, clientID string) *Verifier {
	return newVerifier(oidc.NewRemoteKeySet(ctx, certsURL), clientID)
}

func newVerifier(keys oidc.KeySet, clientID string) *Verifier {
	return &Verifier{v: oidc.NewVerifier(issuer, keys, &oidc.Config{ClientID: clientID})}
}

// Verify returns ErrInvalidToken for a token that fails a check or holds an email Google has not verified.
func (v *Verifier) Verify(ctx context.Context, token string) (Identity, error) {
	t, err := v.v.Verify(ctx, token)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"` //nolint:tagliatelle // Google's claim name
		Name          string `json:"name"`
	}
	if err := t.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims: %w", ErrInvalidToken, err)
	}
	if claims.Email == "" || !claims.EmailVerified {
		return Identity{}, fmt.Errorf("%w: email %q is not verified", ErrInvalidToken, claims.Email)
	}
	return Identity{Subject: t.Subject, Email: strings.ToLower(claims.Email), Name: claims.Name}, nil
}
