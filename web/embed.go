// Package web embeds the HTML templates and static assets of the user interface.
package web

import "embed"

// FS contains the "templates" and "assets" folders.
//
//go:embed templates assets
var FS embed.FS
