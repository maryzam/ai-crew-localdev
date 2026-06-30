package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/maryzam/ai-crew-localdev/internal/quality/sourcecomments"
)

func main() {
	ref := flag.String("ref", "", "git ref to inspect instead of the working tree")
	index := flag.Bool("index", false, "inspect the staged index")
	flag.Parse()
	if *ref != "" && *index {
		fmt.Fprintln(os.Stderr, "-ref and -index are mutually exclusive")
		os.Exit(2)
	}
	paths, err := trackedPaths(*ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	status := 0
	for _, path := range paths {
		if !sourcecomments.Candidate(path) {
			continue
		}
		source, err := readSource(path, *ref, *index)
		if os.IsNotExist(err) && *ref == "" && !*index {
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			status = 2
			continue
		}
		findings, err := sourcecomments.Check(path, source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			status = 2
			continue
		}
		if len(findings) > 0 && status == 0 {
			status = 1
		}
		sourcecomments.Print(os.Stderr, findings)
	}
	os.Exit(status)
}

func trackedPaths(ref string) ([]string, error) {
	args := []string{"ls-files", "-z"}
	if ref != "" {
		args = []string{"ls-tree", "-r", "--name-only", "-z", ref}
	}
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	items := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	paths := make([]string, 0, len(items))
	for _, item := range items {
		if len(item) > 0 {
			paths = append(paths, string(item))
		}
	}
	return paths, nil
}

func readSource(path, ref string, index bool) ([]byte, error) {
	if ref == "" && !index {
		return os.ReadFile(path)
	}
	prefix := ref
	if index {
		prefix = ""
	}
	output, err := exec.Command("git", "show", prefix+":"+path).Output()
	if err != nil {
		return nil, fmt.Errorf("read git object: %w", err)
	}
	return output, nil
}
