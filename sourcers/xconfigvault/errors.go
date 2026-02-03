package xconfigvault

import (
	"errors"
	"fmt"
)

// Common errors returned by the Vault client.
var (
	ErrVaultUnreachable = errors.New("vault: server unreachable")
	ErrAuthFailed       = errors.New("vault: authentication failed")
	ErrSecretNotFound   = errors.New("vault: secret not found")
	ErrKeyNotFound      = errors.New("vault: key not found in secret")
	ErrPermissionDenied = errors.New("vault: permission denied")
	ErrInvalidPath      = errors.New("vault: invalid secret path format")
	ErrClientClosed     = errors.New("vault: client is closed")
	ErrTokenExpired     = errors.New("vault: token expired")
	ErrNoAuthMethod     = errors.New("vault: no authentication method provided")
)

// VaultError wraps errors with additional context.
type VaultError struct {
	Op   string // Operation that failed
	Path string // Secret path involved
	Err  error  // Underlying error
}

func (e *VaultError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("vault %s %s: %v", e.Op, e.Path, e.Err)
	}
	return fmt.Sprintf("vault %s: %v", e.Op, e.Err)
}

func (e *VaultError) Unwrap() error {
	return e.Err
}

func newVaultError(op, path string, err error) error {
	return &VaultError{
		Op:   op,
		Path: path,
		Err:  err,
	}
}
