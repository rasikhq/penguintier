package ztapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rasikhq/penguintier/internal/atomicfile"
)

// TokenLength is the length of a ZeroTier API token. The service generates
// authtoken.secret as 24 random characters from a-z and 0-9.
const TokenLength = 24

// UserTokenFileName is the token copy in the user's home directory.
// zerotier-cli reads this file too, so after "Authorize Penguintier…" the
// user can run zerotier-cli without sudo.
const UserTokenFileName = ".zeroTierOneAuthToken" //nolint:gosec // a file name, not a credential

// ValidToken reports whether s is a well-formed API token: exactly 24
// characters, each a-z or 0-9. It does not trim spaces; callers do that.
func ValidToken(s string) bool {
	if len(s) != TokenLength {
		return false
	}
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'z'
		if !isDigit && !isLower {
			return false
		}
	}
	return true
}

// UserTokenPath returns the path of the user's token copy,
// ~/.zeroTierOneAuthToken.
func UserTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, UserTokenFileName), nil
}

// WriteUserToken saves token to ~/.zeroTierOneAuthToken with mode 0600 and
// returns the file's path. Spaces and newlines around the token are
// removed first (penguintier-token-helper prints a trailing newline).
//
// The token is checked before anything is written, and the file is written
// atomically (see internal/atomicfile): a reader never sees a half-written
// token, and a token file with the wrong mode never exists, not even for a
// moment.
func WriteUserToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if !ValidToken(token) {
		return "", &InvalidTokenError{}
	}
	path, err := UserTokenPath()
	if err != nil {
		return "", err
	}
	if err := atomicfile.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("save API token: %w", err)
	}
	return path, nil
}
