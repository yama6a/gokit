package googleid

import "errors"

// ErrInvalidToken means the ID token failed a check or carries no verified email.
var ErrInvalidToken = errors.New("googleid: invalid id token")
