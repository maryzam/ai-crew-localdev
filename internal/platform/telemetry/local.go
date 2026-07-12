package telemetry

import "github.com/maryzam/ai-crew-localdev/internal/platform/runevents"

const defaultLocalTelemetryMaxBytes = runevents.DefaultLocalMaxBytes

type localSink = runevents.Store

func newLocalSink(path string) (*localSink, error) {
	return newLocalSinkSized(path, defaultLocalTelemetryMaxBytes)
}

func newLocalSinkSized(path string, maxBytes int64) (*localSink, error) {
	return runevents.OpenStoreSized(path, maxBytes)
}
