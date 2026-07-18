package docsexamples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityDocsKeepExplicitContainmentBoundary(t *testing.T) {
	root := repoRoot(t)
	securityDesign := readDoc(t, root, "docs/design/security-design.md")
	userSecurity := readDoc(t, root, "docs/guide/security-for-users.md")
	gapAnalysis := readDoc(t, root, "docs/design/gap-analysis.md")

	for name, content := range map[string]string{
		"security-design":    securityDesign,
		"security-for-users": userSecurity,
		"gap-analysis":       gapAnalysis,
	} {
		normalized := strings.ToLower(content)
		for _, required := range []string{"adversarial", "raw network", "same-uid"} {
			if !strings.Contains(normalized, required) {
				t.Fatalf("%s does not preserve explicit containment boundary term %q", name, required)
			}
		}
	}

	if strings.Contains(gapAnalysis, "| P1 | Containment") {
		t.Fatal("gap analysis still tracks containment as an open P1 after the explicit non-goal decision")
	}
}

func readDoc(t *testing.T, root string, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
