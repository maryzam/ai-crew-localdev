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
	userManual := readDoc(t, root, "docs/guide/user-manual.md")
	gapAnalysis := readDoc(t, root, "docs/design/gap-analysis.md")

	for name, content := range map[string]string{
		"security-design":    securityDesign,
		"security-for-users": userSecurity,
		"user-manual":        userManual,
		"gap-analysis":       gapAnalysis,
	} {
		normalized := strings.ToLower(content)
		for _, required := range []string{"adversarial", "raw network", "same-uid", "spoof"} {
			if !strings.Contains(normalized, required) {
				t.Fatalf("%s does not preserve explicit containment boundary term %q", name, required)
			}
		}
	}

	if !strings.Contains(securityDesign, "| 16 | Ordinary host-native managed runs are rejected before brokered work begins.") || !strings.Contains(securityDesign, "platform/runenv.RequireManagedContainer") {
		t.Fatal("security design must tie host-native managed-run rejection to the code-backed invariant row")
	}
	if !strings.Contains(gapAnalysis, "- The containment P1 is closed by explicit, tested claim boundaries") {
		t.Fatal("gap analysis must record containment as a closed gap with explicit claim boundaries")
	}
	if strings.Contains(gapAnalysis, "| P1 | Containment") || strings.Contains(gapAnalysis, "| P1 | Runtime containment") {
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
