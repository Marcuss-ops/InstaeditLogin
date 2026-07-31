package services

import "strings"

func nonEmptyScopes(raw string) []string {
	return strings.Fields(raw)
}
