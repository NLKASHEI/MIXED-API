//go:build !windows

package gateway

// Linux headless/development builds rely on a private 0700 directory and 0600
// files. Windows release credentials are encrypted with the user's DPAPI key.
func protectSecret(raw []byte) ([]byte, error)   { return raw, nil }
func unprotectSecret(raw []byte) ([]byte, error) { return raw, nil }
