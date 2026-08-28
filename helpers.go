package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// requireFlags returns an error naming every flag whose value is empty.
func requireFlags(flags map[string]string) error {
	var missing []string
	for name, value := range flags {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, "--"+name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
}

// esc escapes a value for safe inclusion in a URL path segment.
func esc(s string) string {
	return url.PathEscape(s)
}
