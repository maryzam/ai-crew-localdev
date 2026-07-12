package runevents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

type UnsupportedSchemaError struct {
	Path                   string
	Line                   int
	SchemaVersion          string
	SupportedSchemaVersion string
}

func (e *UnsupportedSchemaError) Error() string {
	location := "run event"
	if e.Path != "" {
		location = e.Path
	}
	if e.Path != "" && e.Line > 0 {
		location = fmt.Sprintf("%s:%d", e.Path, e.Line)
	}
	return fmt.Sprintf("unsupported run event schema %q at %s; this build supports %q", e.SchemaVersion, location, e.SupportedSchemaVersion)
}

func ReadHistory(path string) ([]RunSummary, error) {
	runs := make(map[string]RunSummary)
	readAny := false
	for _, candidate := range []string{path + ".1", path} {
		file, err := os.Open(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read run event history %s: %w", candidate, err)
		}
		readAny = true
		err = scanHistory(file, runs)
		closeErr := file.Close()
		if err != nil {
			return nil, fmt.Errorf("read run event history %s: %w", candidate, err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if !readAny {
		return nil, nil
	}

	result := make([]RunSummary, 0, len(runs))
	for _, summary := range runs {
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.After(result[j].StartedAt)
	})
	return result, nil
}

func FindRun(runs []RunSummary, id string) (RunSummary, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return RunSummary{}, errors.New("run ID must not be empty")
	}
	var matches []RunSummary
	for _, run := range runs {
		if run.RunID == id || strings.HasPrefix(strings.TrimPrefix(run.RunID, "run_"), strings.TrimPrefix(id, "run_")) {
			matches = append(matches, run)
		}
	}
	switch len(matches) {
	case 0:
		return RunSummary{}, fmt.Errorf("run %q not found", id)
	case 1:
		return matches[0], nil
	default:
		return RunSummary{}, fmt.Errorf("run ID prefix %q is ambiguous", id)
	}
}

func scanHistory(file *os.File, runs map[string]RunSummary) error {
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		event, err := decodeEvent(data)
		if err != nil {
			var unsupported *UnsupportedSchemaError
			if errors.As(err, &unsupported) {
				unsupported.Path = file.Name()
				unsupported.Line = line
				return unsupported
			}
			continue
		}
		runs[event.Run.RunID] = event.Run
	}
	return scanner.Err()
}

func decodeEvent(data []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, err
	}
	if event.SchemaVersion != SchemaVersion {
		return Event{}, &UnsupportedSchemaError{SchemaVersion: event.SchemaVersion, SupportedSchemaVersion: SchemaVersion}
	}
	if event.Run.RunID == "" {
		return Event{}, errors.New("missing run id")
	}
	return event, nil
}
