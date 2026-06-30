package sourcecomments

import (
	"bytes"
	"fmt"
	"go/parser"
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
	return filepath.Ext(path) == ".go" || usesSlashComments(path) || usesHashPath(path) || filepath.Ext(path) == ""
}

func Check(path string, source []byte) ([]Finding, error) {
	if filepath.Ext(path) == ".go" {
		return checkGo(path, source)
	}
	if usesSlashComments(path) {
		return checkSlash(path, source), nil
	}
	if usesHashComments(path, source) {
		return checkHash(path, source), nil
	}
	return nil, nil
}

func Print(w io.Writer, findings []Finding) {
	for _, finding := range findings {
		_, _ = fmt.Fprintf(w, "%s:%d: source comment is not an executable directive: %s\n", finding.Path, finding.Line, finding.Text)
	}
}

func checkGo(path string, source []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse Go source: %w", err)
	}
	lines := bytes.Split(source, []byte{'\n'})
	var findings []Finding
	for _, group := range file.Comments {
		for _, comment := range group.List {
			position := fset.Position(comment.Pos())
			prefix := lines[position.Line-1][:position.Column-1]
			if allowedGo(comment.Text, position.Column, len(bytes.TrimSpace(prefix)) == 0) {
				continue
			}
			findings = append(findings, Finding{Path: path, Line: position.Line, Text: compact(comment.Text)})
		}
	}
	return findings, nil
}

func allowedGo(text string, column int, fullLine bool) bool {
	trimmed := strings.TrimSpace(text)
	return fullLine && (strings.HasPrefix(trimmed, "//go:") || strings.HasPrefix(trimmed, "//line ") || strings.HasPrefix(trimmed, "/*line ") || strings.HasPrefix(trimmed, "// +build ") || column == 1 && generatedMarker(trimmed))
}

func checkHash(path string, source []byte) []Finding {
	var findings []Finding
	inSlashBlock := false
	checkEmbeddedSlash := filepath.Ext(path) == ".yml" || filepath.Ext(path) == ".yaml"
	for indexInFile, raw := range bytes.Split(source, []byte{'\n'}) {
		line := indexInFile + 1
		text := string(raw)
		index := hashCommentIndex(text)
		if checkEmbeddedSlash {
			slashIndex, nextSlashBlock := slashCommentIndex(text, inSlashBlock)
			qualified := slashIndex >= 0 && qualifiedSlashComment(text, slashIndex, inSlashBlock)
			if qualified && (index < 0 || slashIndex < index) {
				index = slashIndex
			}
			inSlashBlock = qualified && nextSlashBlock
		}
		if index < 0 {
			continue
		}
		comment := strings.TrimSpace(text[index:])
		if line == 1 && strings.HasPrefix(comment, "#!") {
			continue
		}
		fullLine := strings.TrimSpace(text[:index]) == ""
		if fullLine && (generatedMarker(comment) || dockerDirective(path, comment)) {
			continue
		}
		findings = append(findings, Finding{Path: path, Line: line, Text: compact(comment)})
	}
	return findings
}

func qualifiedSlashComment(line string, index int, inBlock bool) bool {
	if inBlock {
		return true
	}
	return strings.TrimSpace(line[:index]) == "" || index > 0 && (line[index-1] == ' ' || line[index-1] == '\t')
}

func checkSlash(path string, source []byte) []Finding {
	var findings []Finding
	inBlock := false
	for indexInFile, raw := range bytes.Split(source, []byte{'\n'}) {
		line := indexInFile + 1
		text := string(raw)
		index, block := slashCommentIndex(text, inBlock)
		inBlock = block
		if index < 0 {
			continue
		}
		comment := strings.TrimSpace(text[index:])
		if strings.TrimSpace(text[:index]) == "" && generatedMarker(comment) {
			continue
		}
		findings = append(findings, Finding{Path: path, Line: line, Text: compact(comment)})
	}
	return findings
}

func hashCommentIndex(line string) int {
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
		if current == '#' && (index == 0 || hashDelimiter(line[index-1])) {
			return index
		}
	}
	return -1
}

func hashDelimiter(value byte) bool {
	return value == ' ' || value == '\t' || strings.ContainsRune(";|&()", rune(value))
}

func slashCommentIndex(line string, inBlock bool) (int, bool) {
	if inBlock {
		if end := strings.Index(line, "*/"); end >= 0 {
			return 0, false
		}
		return 0, true
	}
	quote := byte(0)
	escaped := false
	for index := 0; index+1 < len(line); index++ {
		current := line[index]
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' {
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
		pair := line[index : index+2]
		if pair == "//" {
			return index, false
		}
		if pair == "/*" {
			return index, !strings.Contains(line[index+2:], "*/")
		}
	}
	return -1, false
}

func generatedMarker(comment string) bool {
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(comment, "//"), "/*"), "#"))
	text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
	return strings.HasPrefix(text, "Code generated ") && strings.HasSuffix(text, " DO NOT EDIT.")
}

func dockerDirective(path, comment string) bool {
	name := filepath.Base(path)
	if name != "Dockerfile" && !strings.HasPrefix(name, "Dockerfile.") {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(comment, "#")))
	return strings.HasPrefix(text, "syntax=") || strings.HasPrefix(text, "escape=") || strings.HasPrefix(text, "check=")
}

func usesSlashComments(path string) bool {
	switch filepath.Ext(path) {
	case ".c", ".cc", ".cpp", ".cs", ".css", ".dart", ".h", ".hpp", ".java", ".js", ".jsx", ".kt", ".kts", ".php", ".proto", ".rs", ".scala", ".sol", ".swift", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

func usesHashComments(path string, source []byte) bool {
	return usesHashPath(path) || bytes.HasPrefix(source, []byte("#!"))
}

func usesHashPath(path string) bool {
	name := filepath.Base(path)
	if name == "Makefile" || name == "Dockerfile" || name == ".dockerignore" || name == ".gitignore" || strings.HasPrefix(name, "Dockerfile.") || strings.Contains(name, ".env") {
		return true
	}
	switch filepath.Ext(path) {
	case ".awk", ".bash", ".cfg", ".conf", ".ini", ".mk", ".pl", ".properties", ".ps1", ".py", ".r", ".rb", ".service", ".sh", ".socket", ".toml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func compact(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 120 {
		return text[:117] + "..."
	}
	return text
}
