//go:build embedstatic

package main

import (
	"embed"
	"io/fs"
)

//go:embed static
var embeddedStaticFS embed.FS

func staticSource() (fs.FS, bool) {
	sub, err := fs.Sub(embeddedStaticFS, "static")
	if err != nil {
		return nil, false
	}
	return sub, true
}