package sourcecomments

import (
	"bytes"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"path/filepath"
	"strings"
)

type Finding struct {
	Path string
	Line int
	Text string
}

func Candidate(path string) bool {
	hash, slash := commentStyles(path)
	return filepath.Ext(path) == ".go" || hash || slash
}

func Check(path string, source []byte) ([]Finding, error) {
	if filepath.Ext(path) == ".go" {
		return checkGo(path, source)
	}
	hash, slash := commentStyles(path)
	return checkLines(path, source, hash, slash), nil
}

func Print(w io.Writer, findings []Finding) {
	for _, finding := range findings {
		_, _ = fmt.Fprintf(w, "%s:%d: source comment is not an executable directive: %s\n", finding.Path, finding.Line, finding.Text)
	}
}

func checkGo(path string, source []byte) ([]Finding, error) {
	files := token.NewFileSet()
	file := files.AddFile(path, files.Base(), len(source))
	var parseError error
	var lexer scanner.Scanner
	lexer.Init(file, source, func(position token.Position, message string) {
		parseError = fmt.Errorf("parse Go source at %d:%d: %s", position.Line, position.Column, message)
	}, scanner.ScanComments)
	lines := bytes.Split(source, []byte{'\n'})
	var findings []Finding
	for {
		position, kind, text := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind != token.COMMENT {
			continue
		}
		location := file.Position(position)
		prefix := lines[location.Line-1][:location.Column-1]
		fullLine := len(bytes.TrimSpace(prefix)) == 0
		trimmed := strings.TrimSpace(text)
		allowed := fullLine && (strings.HasPrefix(trimmed, "//go:") || strings.HasPrefix(trimmed, "//line ") || location.Column == 1 && generatedMarker(trimmed))
		if !allowed {
			findings = append(findings, Finding{Path: path, Line: location.Line, Text: compact(text)})
		}
	}
	return findings, parseError
}

func checkLines(path string, source []byte, hash, slash bool) []Finding {
	var findings []Finding
	block := false
	for index, raw := range bytes.Split(source, []byte{'\n'}) {
		text := string(raw)
		commentIndex, nextBlock := findComment(text, block, hash, slash)
		block = nextBlock
		if commentIndex < 0 {
			continue
		}
		comment := strings.TrimSpace(text[commentIndex:])
		fullLine := len(strings.TrimSpace(text[:commentIndex])) == 0
		allowed := index == 0 && strings.HasPrefix(comment, "#!") || fullLine && (generatedMarker(comment) || dockerDirective(path, comment))
		if !allowed {
			findings = append(findings, Finding{Path: path, Line: index + 1, Text: compact(comment)})
		}
	}
	return findings
}

func findComment(line string, block, hash, slash bool) (int, bool) {
	if block {
		return 0, !strings.Contains(line, "*/")
	}
	quote := byte(0)
	escaped := false
	for index := 0; index < len(line); index++ {
		current := line[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		delimited := index == 0 || line[index-1] == ' ' || line[index-1] == '\t' || strings.ContainsRune(";|&()", rune(line[index-1]))
		if hash && current == '#' && delimited {
			return index, false
		}
		if slash && index+1 < len(line) && delimited {
			switch line[index : index+2] {
			case "//":
				return index, false
			case "/*":
				return index, !strings.Contains(line[index+2:], "*/")
			}
		}
	}
	return -1, false
}

func commentStyles(path string) (hash, slash bool) {
	name := filepath.Base(path)
	switch name {
	case "Makefile", "Dockerfile", ".dockerignore", ".gitignore", "pre-commit", "pre-push", "commit-msg":
		hash = true
	}
	switch filepath.Ext(path) {
	case ".sh", ".toml", ".service", ".socket", ".example":
		hash = true
	case ".yaml", ".yml":
		hash, slash = true, true
	case ".c", ".cc", ".cpp", ".css", ".h", ".hpp", ".java", ".js", ".jsx", ".rs", ".ts", ".tsx":
		slash = true
	}
	return hash, slash
}

func generatedMarker(comment string) bool {
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(comment, "//"), "/*"), "#"))
	text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
	return strings.HasPrefix(text, "Code generated ") && strings.HasSuffix(text, " DO NOT EDIT.")
}

func dockerDirective(path, comment string) bool {
	if filepath.Base(path) != "Dockerfile" {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(comment, "#")))
	return strings.HasPrefix(text, "syntax=") || strings.HasPrefix(text, "escape=") || strings.HasPrefix(text, "check=")
}

func compact(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 120 {
		return text[:117] + "..."
	}
	return text
}
