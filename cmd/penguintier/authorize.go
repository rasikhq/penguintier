package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rasikhq/penguintier/internal/tray"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// tokenHelper is the small root helper that prints the ZeroTier API token.
// pkexec runs it after the user typed their password into the desktop's
// polkit prompt (action io.github.rasikhq.Penguintier.read-token).
const tokenHelper = "penguintier-token-helper"

// pkexec's own exit codes. Any other code is the helper's.
const (
	pkexecDismissed     = 126 // the user closed the password prompt
	pkexecNotAuthorized = 127 // wrong password, not allowed, or no polkit agent
)

// The token helper's exit codes (cmd/penguintier-token-helper).
const (
	helperCannotRead   = 1 // the token file can't be read (ZeroTier missing?)
	helperInvalidToken = 3 // the file holds no valid token
)

// doctorHint ends the messages for failures the user has to work around
// by hand: `penguintier doctor` prints the manual command. (Go error
// texts don't end with a period, so neither does this.)
const doctorHint = " Run “penguintier doctor” in a terminal for the manual way"

// authorize runs `pkexec penguintier-token-helper` and saves the token it
// prints to ~/.zeroTierOneAuthToken (mode 0600). The tray then connects
// again with the new token on its next poll.
//
// It returns tray.ErrCancelled when the user dismissed the prompt.
func authorize(ctx context.Context) error {
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return errors.New("pkexec is not installed." + doctorHint)
	}
	helper, err := tray.FindHelper(tokenHelper)
	if err != nil {
		return fmt.Errorf("%w.%s", err, doctorHint)
	}

	// The command runs until the user answers the prompt. ctx stops it if
	// the tray quits first.
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, pkexec, helper) //nolint:gosec // pkexec and our own helper, found in fixed places
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	// errors.As finds the *exec.ExitError inside err when the program ran
	// and exited with a non-zero code.
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		if _, err := ztapi.WriteUserToken(stdout.String()); err != nil {
			return fmt.Errorf("save the token: %w", err)
		}
		return nil
	case !errors.As(err, &exitErr):
		return fmt.Errorf("run pkexec: %w", err)
	}

	switch exitErr.ExitCode() {
	case pkexecDismissed:
		return tray.ErrCancelled
	case pkexecNotAuthorized:
		return errors.New("not allowed to read ZeroTier's API token " +
			"(wrong password, or no polkit agent is running)." + doctorHint)
	case helperCannotRead:
		return fmt.Errorf("ZeroTier's token file can't be read. Is ZeroTier One installed and running? (%s)", firstLine(stderr.String()))
	case helperInvalidToken:
		return errors.New("ZeroTier's token file holds no valid token. Restarting ZeroTier One makes a new one: sudo systemctl restart zerotier-one")
	default:
		return fmt.Errorf("pkexec %s failed (exit code %d): %s", helper, exitErr.ExitCode(), firstLine(stderr.String()))
	}
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "no details"
}
