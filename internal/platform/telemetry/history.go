package telemetry

import "github.com/maryzam/ai-crew-localdev/internal/platform/runevents"

func ReadRunHistory(path string) ([]RunSummary, error) {
	return runevents.ReadHistory(path)
}

func FindRun(runs []RunSummary, id string) (RunSummary, error) {
	return runevents.FindRun(runs, id)
}
