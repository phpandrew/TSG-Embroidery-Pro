// Package fonts holds glyph-library lettering fonts compiled into the app.
//
// Add a font by creating a folder here, e.g. fonts/Varsity/, containing
// font.json and one DST per letter (see README.md). It is built into the exe
// and appears in the Lettering font list for every install.
package fonts

import "embed"

//go:embed *
var FS embed.FS
