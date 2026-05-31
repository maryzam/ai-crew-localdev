package inlinecomments

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strings"
)

type Finding struct {
	Path string
	Line int
	Text string
}

type bodyRange struct {
	start token.Pos
	end   token.Pos
}

func CheckFile(path string, addedLines map[int]struct{}) ([]Finding, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse Go file: %w", err)
	}
	if ast.IsGenerated(parsed) {
		return nil, nil
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

	var findings []Finding
	for _, group := range parsed.Comments {
		if allowedDirective(group) || !insideAnyBody(group.Pos(), bodies) || !touchesAddedLine(fset, group, addedLines) {
			continue
		}
		pos := fset.Position(group.Pos())
		findings = append(findings, Finding{
			Path: path,
			Line: pos.Line,
			Text: firstLine(group.Text()),
		})
	}
	return findings, nil
}

func PrintFindings(w io.Writer, findings []Finding) {
	for _, finding := range findings {
		_, _ = fmt.Fprintf(w, "%s:%d: inline comment inside function body: %s\n", finding.Path, finding.Line, finding.Text)
	}
}

func insideAnyBody(pos token.Pos, bodies []bodyRange) bool {
	for _, body := range bodies {
		if pos > body.start && pos < body.end {
			return true
		}
	}
	return false
}

func touchesAddedLine(fset *token.FileSet, group *ast.CommentGroup, addedLines map[int]struct{}) bool {
	if len(addedLines) == 0 {
		return false
	}
	for _, comment := range group.List {
		start := fset.Position(comment.Pos()).Line
		end := fset.Position(comment.End()).Line
		for line := start; line <= end; line++ {
			if _, ok := addedLines[line]; ok {
				return true
			}
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
