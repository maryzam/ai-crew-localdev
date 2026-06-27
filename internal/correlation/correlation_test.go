package correlation

import (
	"strings"
	"testing"
)

func TestValidateCorrelationBoundaries(t *testing.T) {
	for _, runID := range []string{"", "run_123", "run_" + strings.Repeat("a", MaxRunIDLength-len(RunIDPrefix))} {
		if err := ValidateRunID(runID); err != nil {
			t.Errorf("ValidateRunID(%q): %v", runID, err)
		}
	}
	for _, runID := range []string{"missing-prefix", "run_with space", "run_" + strings.Repeat("a", MaxRunIDLength)} {
		if err := ValidateRunID(runID); err == nil {
			t.Errorf("ValidateRunID(%q) succeeded", runID)
		}
	}

	if err := ValidateTaskRef(strings.Repeat("a", MaxTaskRefLength)); err != nil {
		t.Fatal(err)
	}
	for _, taskRef := range []string{strings.Repeat("a", MaxTaskRefLength+1), "github:owner/repo #43", "github:owner/répo#43"} {
		if err := ValidateTaskRef(taskRef); err == nil {
			t.Errorf("ValidateTaskRef(%q) succeeded", taskRef)
		}
	}
}
