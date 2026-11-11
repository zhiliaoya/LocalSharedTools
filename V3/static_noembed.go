//go:build !embedstatic

package main

import "io/fs"

func staticSource() (fs.FS, bool) {
	return nil, false
}
