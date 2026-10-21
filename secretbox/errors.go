package secretbox

import "errors"

var (
	ErrMalformedKey = errors.New("secretbox: malformed key")
	ErrDecrypt      = errors.New("secretbox: decrypt failed")
)
