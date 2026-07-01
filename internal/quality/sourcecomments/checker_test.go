package sourcecomments

import "testing"

func TestGoAllowsOnlyExecutableDirectives(t *testing.T) {
	source := []byte("//go:build linux\n// Code generated fixture; DO NOT EDIT.\npackage sample\n//nolint:gosec\nfunc value() {} // explanation\nfunc generated() {} // Code generated fixture; DO NOT EDIT.\nfunc directive() {} //go:noinline\n")
	findings, err := Check("sample.go", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 4 || findings[0].Line != 4 || findings[1].Line != 5 || findings[2].Line != 6 || findings[3].Line != 7 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestHashCommentsPreserveValuesAndRejectProse(t *testing.T) {
	source := []byte("#!/usr/bin/env bash\nurl='https://example.test/#fragment'\ncount=${#values[@]}\necho ok # explanation\n")
	findings, err := Check("script.sh", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Line != 4 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestDockerAllowsParserDirectives(t *testing.T) {
	source := []byte("# syntax=docker/dockerfile:1\n# explanation\nFROM scratch\n")
	findings, err := Check("Dockerfile", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Line != 2 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestYAMLRejectsEmbeddedSlashCommentsWithoutTreatingURLAsComment(t *testing.T) {
	source := []byte("endpoint: https://example.test/path\nscript: |\n  rm -rf path/*\n  value(); // explanation\n  // explanation\n")
	findings, err := Check("workflow.yml", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Line != 4 || findings[1].Line != 5 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestSlashCommentsRejectInlineAndBlocks(t *testing.T) {
	source := []byte("const url = \"https://example.test\";\nvalue(); // explanation\n/* block\ncontinued */\n")
	findings, err := Check("sample.js", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %#v", findings)
	}
}
