package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:phoenix
var embedded embed.FS

const PhoenixDir = "phoenix"

func Phoenix() (fs.FS, error) {
	return fs.Sub(embedded, PhoenixDir)
}
