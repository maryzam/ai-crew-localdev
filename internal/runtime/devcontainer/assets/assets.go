package assets

import (
	"embed"
	"io/fs"
)

//go:embed generic
var embedded embed.FS

const (
	GenericDir     = "generic"
	ExecutableMode = fs.FileMode(0o755)
	DataMode       = fs.FileMode(0o644)
)

func Generic() (fs.FS, error) {
	return fs.Sub(embedded, GenericDir)
}

func Mode(name string) fs.FileMode {
	if name == "entrypoint.sh" {
		return ExecutableMode
	}
	return DataMode
}
