package portability

import "errors"

var (
	ErrBadPassphrase = errors.New("portability: bad passphrase or tampered bundle")
	ErrConflict      = errors.New("portability: target path conflict (use --force)")
	ErrIntegrity     = errors.New("portability: manifest integrity check failed")
	ErrVersion       = errors.New("portability: bundle version unsupported")
	ErrCrossDevice   = errors.New("portability: temp dir and target on different partitions")
)
