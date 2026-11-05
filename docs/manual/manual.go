// Package manual embeds the user manual Markdown documents for the dashboard.
package manual

import "embed"

// Files contains all Markdown files in this directory.
//
//go:embed *.md
var Files embed.FS
