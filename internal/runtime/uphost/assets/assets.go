package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:langfuse
var embedded embed.FS

const LangfuseDir = "langfuse"

func Langfuse() (fs.FS, error) {
	return fs.Sub(embedded, LangfuseDir)
}
