package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// Where things come from. Under systemd (packaging/systemd/
// penguintier-dnsd.service) every value has a fixed source; the
// environment overrides are for running the daemon by hand during
// development.
const (
	// credentialName is the name in the unit's
	// LoadCredential=zt-authtoken:/var/lib/zerotier-one/authtoken.secret.
	// systemd copies the file into $CREDENTIALS_DIRECTORY under this name
	// before starting us, so we never need read access to
	// /var/lib/zerotier-one ourselves.
	credentialName = "zt-authtoken" //nolint:gosec // a credential name, not a credential

	// defaultRuntimeDir is used when $RUNTIME_DIRECTORY is not set (the
	// unit's RuntimeDirectory= sets it).
	defaultRuntimeDir = "/run/penguintier-dnsd"

	// maxCredentialSize is far more than a 24-character token.
	maxCredentialSize = 4096
)

// config is everything the daemon needs to start.
type config struct {
	apiURL    string // e.g. "http://127.0.0.1:9993"
	token     string
	statePath string // e.g. "/run/penguintier-dnsd/applied.json"
}

// loadConfig works out the config from the environment:
//
//   - API token: the file in $PENGUINTIER_TOKEN_FILE if set, else the
//     systemd credential $CREDENTIALS_DIRECTORY/zt-authtoken.
//   - API address: $PENGUINTIER_API_URL if set, else 127.0.0.1 and the port
//     from /var/lib/zerotier-one/zerotier-one.port (9993 if missing).
//   - State file: in $RUNTIME_DIRECTORY, else /run/penguintier-dnsd.
func loadConfig() (config, error) {
	var cfg config

	token, err := loadToken()
	if err != nil {
		return config{}, err
	}
	cfg.token = token

	// APIURLs looks at $PENGUINTIER_API_URL and zerotier-one.port.
	urls, err := ztapi.APIURLs()
	if err != nil {
		return config{}, err
	}
	// The service listens on 127.0.0.1 (the first URL); the [::1]
	// fallback the tray tries is not needed for a system service.
	cfg.apiURL = urls[0]

	dir := os.Getenv("RUNTIME_DIRECTORY")
	if dir == "" {
		dir = defaultRuntimeDir
	}
	// With several RuntimeDirectory= entries systemd joins them with ":".
	// We have one, but take the first to be safe.
	dir, _, _ = strings.Cut(dir, ":")
	cfg.statePath = filepath.Join(dir, stateFileName)
	return cfg, nil
}

// loadToken reads the ZeroTier API token.
func loadToken() (string, error) {
	// For development: a token file of your own. ReadTokenFile insists
	// that only you can read it.
	if path := os.Getenv(ztapi.EnvTokenFile); path != "" {
		token, err := ztapi.ReadTokenFile(path)
		if err != nil {
			return "", fmt.Errorf("%s: %w", ztapi.EnvTokenFile, err)
		}
		return token, nil
	}

	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return "", fmt.Errorf("no API token: run penguintier-dnsd as its systemd service "+
			"(LoadCredential=%s:...), or set %s for testing", credentialName, ztapi.EnvTokenFile)
	}
	return readCredential(filepath.Join(dir, credentialName))
}

// readCredential reads a token from a systemd credential file. systemd
// owns these files and makes them readable for us only, so unlike
// ztapi.ReadTokenFile there is no owner check here.
func readCredential(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is our own credential, from $CREDENTIALS_DIRECTORY
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("API token credential %s is missing (is LoadCredential=%s:... in the unit?): %w", path, credentialName, err)
	}
	if err != nil {
		return "", fmt.Errorf("open API token credential: %w", err)
	}
	defer f.Close() // read-only: a close error can't lose data

	data, err := io.ReadAll(io.LimitReader(f, maxCredentialSize))
	if err != nil {
		return "", fmt.Errorf("read API token credential %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if !ztapi.ValidToken(token) {
		return "", &ztapi.InvalidTokenError{Path: path}
	}
	return token, nil
}
