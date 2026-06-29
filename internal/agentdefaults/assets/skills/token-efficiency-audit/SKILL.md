---
name: token-efficiency-audit
description: Measure whether a workflow change reduces tokens without reducing quality.
---

# Token efficiency audit

Use this skill only for a planned efficiency review.

1. Choose comparable tasks and one unchanged quality gate.
2. Record at least five runs when practical.
3. Change one variable.
4. Compare median input, output, cache-read, and total tokens.
5. Reject the change if success, security evidence, or review quality falls.
6. Record the tool version, date range, sample size, and result.

Use managed-run history as the source for token and cost analysis.
