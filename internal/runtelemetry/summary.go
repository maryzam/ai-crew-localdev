package runtelemetry

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

type Summary struct {
	Run            string
	TelemetryBytes int64
	Records        map[string]int
	Forwarder      *Stats
	Pruned         []evidence.PrunedTelemetry
	Collector      map[string]int64
}

var collectorCounters = map[string]string{
	"otelcol_receiver_accepted_log_records":      "receiver",
	"otelcol_receiver_accepted_metric_points":    "receiver",
	"otelcol_receiver_accepted_spans":            "receiver",
	"otelcol_receiver_refused_log_records":       "receiver",
	"otelcol_receiver_refused_metric_points":     "receiver",
	"otelcol_receiver_refused_spans":             "receiver",
	"otelcol_receiver_failed_log_records":        "receiver",
	"otelcol_receiver_failed_metric_points":      "receiver",
	"otelcol_receiver_failed_spans":              "receiver",
	"otelcol_exporter_sent_log_records":          "exporter",
	"otelcol_exporter_sent_metric_points":        "exporter",
	"otelcol_exporter_sent_spans":                "exporter",
	"otelcol_exporter_send_failed_log_records":   "exporter",
	"otelcol_exporter_send_failed_metric_points": "exporter",
	"otelcol_exporter_send_failed_spans":         "exporter",
}

var collectorComponents = map[string]string{"receiver": "otlp", "exporter": "file/runs"}

func Summarize(root *evidence.Root, run string) (Summary, error) {
	s := Summary{Run: run, Records: map[string]int{}, Collector: map[string]int64{}}
	telemetry, err := root.TelemetryPath(run)
	if err != nil {
		return s, err
	}
	if info, err := os.Stat(telemetry); err == nil {
		s.TelemetryBytes = info.Size()
		if err := eachLine(telemetry, func(line []byte) error { return countRecords(line, s.Records) }); err != nil {
			return s, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	dir, _ := root.RunDir(run)
	if err := eachLine(filepath.Join(dir, evidence.RunFile), func(line []byte) error {
		var event struct {
			Event string `json:"event"`
			Data  Stats  `json:"data"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		if event.Event == ForwarderStatsEvent {
			stats := event.Data
			s.Forwarder = &stats
		}
		return nil
	}); err != nil {
		return s, err
	}
	if err := eachLine(root.PruningPath(), func(line []byte) error {
		var p evidence.PrunedTelemetry
		if err := json.Unmarshal(line, &p); err != nil {
			return err
		}
		if p.Run == run {
			s.Pruned = append(s.Pruned, p)
		}
		return nil
	}); err != nil {
		return s, err
	}
	err = eachLine(root.CollectorMetricsPath(), func(line []byte) error { return latestCounters(line, s.Collector) })
	return s, err
}

func eachLine(path string, visit func([]byte) error) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(line) > 1 {
			if visitErr := visit(line); visitErr != nil {
				return fmt.Errorf("%s line %d: %w", path, n, visitErr)
			}
		}
	}
}

func countRecords(line []byte, records map[string]int) error {
	var payload struct {
		ResourceLogs []struct {
			ScopeLogs []struct {
				LogRecords []json.RawMessage `json:"logRecords"`
			} `json:"scopeLogs"`
		} `json:"resourceLogs"`
		ResourceMetrics []struct {
			ScopeMetrics []struct {
				Metrics []json.RawMessage `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []json.RawMessage `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return err
	}
	for _, r := range payload.ResourceLogs {
		for _, s := range r.ScopeLogs {
			records["logs"] += len(s.LogRecords)
		}
	}
	for _, r := range payload.ResourceMetrics {
		for _, s := range r.ScopeMetrics {
			records["metrics"] += len(s.Metrics)
		}
	}
	for _, r := range payload.ResourceSpans {
		for _, s := range r.ScopeSpans {
			records["spans"] += len(s.Spans)
		}
	}
	return nil
}

func latestCounters(line []byte, counters map[string]int64) error {
	var payload struct {
		ResourceMetrics []struct {
			ScopeMetrics []struct {
				Metrics []struct {
					Name string `json:"name"`
					Sum  struct {
						DataPoints []struct {
							Attributes []struct {
								Key   string `json:"key"`
								Value struct {
									StringValue string `json:"stringValue"`
								} `json:"value"`
							} `json:"attributes"`
							AsInt json.RawMessage `json:"asInt"`
						} `json:"dataPoints"`
					} `json:"sum"`
				} `json:"metrics"`
			} `json:"scopeMetrics"`
		} `json:"resourceMetrics"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return err
	}
	for _, r := range payload.ResourceMetrics {
		for _, s := range r.ScopeMetrics {
			for _, m := range s.Metrics {
				kind, tracked := collectorCounters[m.Name]
				if !tracked {
					continue
				}
				for _, p := range m.Sum.DataPoints {
					component := ""
					for _, a := range p.Attributes {
						if a.Key == kind {
							component = a.Value.StringValue
						}
					}
					if component != collectorComponents[kind] {
						continue
					}
					value, err := strconv.ParseInt(string(trimQuotes(p.AsInt)), 10, 64)
					if err != nil {
						return fmt.Errorf("%s: %w", m.Name, err)
					}
					counters[m.Name] = value
				}
			}
		}
	}
	return nil
}

func trimQuotes(raw json.RawMessage) []byte {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1]
	}
	return raw
}
