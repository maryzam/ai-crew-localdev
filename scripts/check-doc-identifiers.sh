#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cat >"$tmpdir/extract_broker_identifiers.go" <<'GO'
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "internal/broker/api.go", nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse internal/broker/api.go: %v\n", err)
		os.Exit(2)
	}

	identifiers := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if !isBrokerWireIdentifier(name.Name) {
					continue
				}
				if i >= len(valueSpec.Values) {
					fmt.Fprintf(os.Stderr, "%s: broker wire identifier must have an explicit string value\n", name.Name)
					os.Exit(2)
				}
				lit, ok := valueSpec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					fmt.Fprintf(os.Stderr, "%s: broker wire identifier must be a string constant\n", name.Name)
					os.Exit(2)
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: unquote string constant: %v\n", name.Name, err)
					os.Exit(2)
				}
				identifiers[value] = true
			}
		}
	}

	var values []string
	for value := range identifiers {
		values = append(values, value)
	}
	sort.Strings(values)
	for _, value := range values {
		fmt.Println(value)
	}
}

func isBrokerWireIdentifier(name string) bool {
	return strings.HasPrefix(name, "Method") ||
		strings.HasPrefix(name, "ErrCode") ||
		strings.HasPrefix(name, "CredentialType")
}
GO

go run "$tmpdir/extract_broker_identifiers.go" >"$tmpdir/active-identifiers.txt"
mapfile -t ACTIVE_IDENTIFIERS <"$tmpdir/active-identifiers.txt"

if (( ${#ACTIVE_IDENTIFIERS[@]} == 0 )); then
  printf 'no active broker wire identifiers found in internal/broker/api.go\n' >&2
  exit 1
fi

LEGACY_IDENTIFIERS=(
  allowed_repos
  mint_token
  repo_not_allowed
)

LEGACY_ALLOWLIST=(
  "docs/decisions/0001-credential-generic-broker-api.md:allowed_repos"
  "docs/decisions/0001-credential-generic-broker-api.md:mint_token"
)

is_legacy_allowed() {
  local file="$1"
  local ident="$2"
  local allowed
  for allowed in "${LEGACY_ALLOWLIST[@]}"; do
    if [[ "$allowed" == "$file:$ident" ]]; then
      return 0
    fi
  done
  return 1
}

status=0

contains_identifier() {
  local ident="$1"
  shift
  grep -R -F -q -- "$ident" "$@"
}

find_doc_identifier_refs() {
  local ident="$1"
  shift
  grep -R -n -F -- "$ident" "$@" || true
}

for ident in "${ACTIVE_IDENTIFIERS[@]}"; do
  if ! contains_identifier "$ident" internal/broker internal/brokerclient; then
    printf 'active broker identifier %q is documented as allowed but was not found in broker code\n' "$ident" >&2
    status=1
  fi
done

mapfile -t docs < <(find docs -type f -name '*.md' -print | sort)
docs+=(README.md)

for ident in "${LEGACY_IDENTIFIERS[@]}"; do
  while IFS=: read -r file line text; do
    [[ -n "$file" ]] || continue
    if is_legacy_allowed "$file" "$ident"; then
      continue
    fi
    printf '%s:%s: stale broker identifier %q found: %s\n' "$file" "$line" "$ident" "$text" >&2
    status=1
  done < <(find_doc_identifier_refs "$ident" "${docs[@]}")
done

if (( status != 0 )); then
  printf '\nUpdate the docs to use current broker wire identifiers, or add a narrow historical exception near LEGACY_ALLOWLIST.\n' >&2
fi

exit "$status"
