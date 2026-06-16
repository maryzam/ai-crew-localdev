package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/quality/inlinecomments"
)

func main() {
	addedLinesPath := flag.String("added-lines", "", "path to newline-delimited file:line entries")
	ref := flag.String("ref", "", "git ref to read Go files from; defaults to the working tree")
	flag.Parse()

	if *addedLinesPath == "" {
		fmt.Fprintln(os.Stderr, "-added-lines is required")
		os.Exit(2)
	}

	addedLines, err := readAddedLines(*addedLinesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read added lines: %v\n", err)
		os.Exit(2)
	}

	status := 0
	for _, path := range flag.Args() {
		findings, err := checkPath(path, *ref, addedLines[path])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			status = 2
			continue
		}
		if len(findings) > 0 && status == 0 {
			status = 1
		}
		inlinecomments.PrintFindings(os.Stderr, findings)
	}
	os.Exit(status)
}

func checkPath(path, ref string, addedLines map[int]struct{}) ([]inlinecomments.Finding, error) {
	if ref == "" {
		return inlinecomments.CheckFile(path, addedLines)
	}
	source, err := readGitBlob(ref, path)
	if err != nil {
		return nil, err
	}
	return inlinecomments.CheckSource(path, source, addedLines)
}

func readGitBlob(ref, path string) ([]byte, error) {
	output, err := exec.Command("git", "show", ref+":"+path).Output()
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", path, ref, err)
	}
	return output, nil
}

func readAddedLines(path string) (map[string]map[int]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	addedLines := make(map[string]map[int]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		entry := scanner.Text()
		if entry == "" {
			continue
		}
		filePath, lineText, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("invalid added-line entry %q", entry)
		}
		line, err := strconv.Atoi(lineText)
		if err != nil {
			return nil, fmt.Errorf("invalid line in %q: %w", entry, err)
		}
		if addedLines[filePath] == nil {
			addedLines[filePath] = make(map[int]struct{})
		}
		addedLines[filePath][line] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return addedLines, nil
}
