// Package examples embeds the local data formats shipped with every CLI binary.
package examples

import "embed"

// Files contains reference data, not live profiles, workflows, or run results.
//
//go:embed example-profile example-run example-workflow
var Files embed.FS
