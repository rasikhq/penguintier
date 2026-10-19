package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

const testToken = "abcdefghijklmnopqrstuvwx" //nolint:gosec // a made-up test token

func TestLoadConfig(t *testing.T) {
	creds := t.TempDir()
	if err := os.WriteFile(filepath.Join(creds, credentialName), []byte(testToken+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	badCreds := t.TempDir()
	if err := os.WriteFile(filepath.Join(badCreds, credentialName), []byte("not a token"), 0o400); err != nil {
		t.Fatal(err)
	}
	ownToken := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(ownToken, []byte(testToken), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    config
		wantErr string // part of the error message; "" for no error
	}{
		{
			name: "systemd credential and runtime directory",
			env: map[string]string{
				"CREDENTIALS_DIRECTORY": creds,
				"RUNTIME_DIRECTORY":     "/run/penguintier-dnsd",
				"PENGUINTIER_API_URL":   "http://127.0.0.1:9993",
			},
			want: config{apiURL: "http://127.0.0.1:9993", token: testToken, statePath: "/run/penguintier-dnsd/applied.json"},
		},
		{
			name: "token file for development, default state directory",
			env:  map[string]string{"PENGUINTIER_TOKEN_FILE": ownToken, "PENGUINTIER_API_URL": "http://127.0.0.1:19993/"},
			want: config{apiURL: "http://127.0.0.1:19993", token: testToken, statePath: "/run/penguintier-dnsd/applied.json"},
		},
		{
			name: "first of several runtime directories",
			env:  map[string]string{"CREDENTIALS_DIRECTORY": creds, "RUNTIME_DIRECTORY": "/run/a:/run/b", "PENGUINTIER_API_URL": "http://127.0.0.1:9993"},
			want: config{apiURL: "http://127.0.0.1:9993", token: testToken, statePath: "/run/a/applied.json"},
		},
		{
			name:    "no token anywhere",
			env:     map[string]string{},
			wantErr: "no API token",
		},
		{
			name:    "credential missing",
			env:     map[string]string{"CREDENTIALS_DIRECTORY": t.TempDir()},
			wantErr: "is missing",
		},
		{
			name:    "credential is not a token",
			env:     map[string]string{"CREDENTIALS_DIRECTORY": badCreds},
			wantErr: "invalid ZeroTier API token",
		},
		{
			name:    "bad API URL",
			env:     map[string]string{"CREDENTIALS_DIRECTORY": creds, "PENGUINTIER_API_URL": "ftp://x"},
			wantErr: "PENGUINTIER_API_URL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv sets a variable for this test only. Clear the ones
			// we read first, so the developer's own environment can't leak
			// in.
			for _, key := range []string{"CREDENTIALS_DIRECTORY", "RUNTIME_DIRECTORY", ztapi.EnvTokenFile, ztapi.EnvAPIURL} {
				t.Setenv(key, "")
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			got, err := loadConfig()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("loadConfig() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("loadConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
