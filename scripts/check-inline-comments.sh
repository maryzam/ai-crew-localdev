#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

BASE_REF="${1:-${BASE_REF:-origin/main}}"
HEAD_REF="${2:-${HEAD_REF:-HEAD}}"
MERGE_BASE="$(git merge-base "$BASE_REF" "$HEAD_REF")"

mapfile -t go_files < <(
  git diff --name-only --diff-filter=ACMR "$MERGE_BASE" "$HEAD_REF" -- '*.go'
)

if (( ${#go_files[@]} == 0 )); then
  exit 0
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cat >"$tmpdir/check_inline_comments.go" <<'GO'
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

type bodyRange struct {
	start token.Pos
	end   token.Pos
}

func main() {
	status := 0
	for _, file := range os.Args[1:] {
		if file == "--" {
			continue
		}
		fileStatus := checkFile(file)
		if fileStatus > status {
			status = fileStatus
		}
	}
	os.Exit(status)
}

func checkFile(path string) int {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: parse Go file: %v\n", path, err)
		return 2
	}
	if ast.IsGenerated(parsed) {
		return 0
	}

	var bodies []bodyRange
	ast.Inspect(parsed, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				bodies = append(bodies, bodyRange{start: fn.Body.Lbrace, end: fn.Body.Rbrace})
			}
		case *ast.FuncLit:
			if fn.Body != nil {
				bodies = append(bodies, bodyRange{start: fn.Body.Lbrace, end: fn.Body.Rbrace})
			}
		}
		return true
	})

	status := 0
	for _, group := range parsed.Comments {
		if allowedDirective(group) || !insideAnyBody(group.Pos(), bodies) {
			continue
		}
		pos := fset.Position(group.Pos())
		fmt.Fprintf(os.Stderr, "%s:%d: inline comment inside function body: %s\n", path, pos.Line, firstLine(group.Text()))
		status = 1
	}
	return status
}

func insideAnyBody(pos token.Pos, bodies []bodyRange) bool {
	for _, body := range bodies {
		if pos > body.start && pos < body.end {
			return true
		}
	}
	return false
}

func allowedDirective(group *ast.CommentGroup) bool {
	for _, comment := range group.List {
		text := strings.TrimSpace(comment.Text)
		if strings.HasPrefix(text, "//go:") ||
			strings.HasPrefix(text, "// +build") ||
			strings.HasPrefix(text, "//line ") ||
			text == "//nolint" ||
			strings.HasPrefix(text, "//nolint ") ||
			strings.HasPrefix(text, "//nolint:") ||
			strings.HasPrefix(text, "//lint:ignore ") {
			continue
		}
		return false
	}
	return true
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 100 {
		return line[:100] + "..."
	}
	return line
}
GO

go run "$tmpdir/check_inline_comments.go" -- "${go_files[@]}"
