package main

import (
	"fmt"
	"strings"
)

func validatePrivacyStatus(value string) error {
	switch value {
	case "public", "unlisted", "private":
		return nil
	default:
		return fmt.Errorf("--privacy must be public, unlisted or private (got %q)", value)
	}
}

func validateNoExtraArgs(command string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s: unexpected argument %q", command, args[0])
	}
	return nil
}

func validateRequiredPath(flagName, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s is required", flagName)
	}
	return nil
}
