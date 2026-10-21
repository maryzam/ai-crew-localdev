package telemetry

import (
	"bytes"
	"net/http"
	"testing"
)

func TestNativeRelayRejectsOversizedPayload(t *testing.T) {
	logPath := t.TempDir() + "/runs.jsonl"
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	recorder, err := StartRun(RunContext{RunID: "run_oversized", AgentName: "claude", Repo: "owner/repo", AgentCommand: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := StartNativeRelay(recorder, nativeExporterFunc(func(string, []byte) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		relay.Close()
		_ = recorder.Close()
	})

	oversized := bytes.Repeat([]byte("a"), nativeRequestLimit+1)
	request, err := http.NewRequest(http.MethodPost, relay.Endpoint()+"/v1/traces", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", relay.Authorization())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func representativeEventFinished() Event {
	event := representativeEvent()
	event.EventType = "agent.command.finished"
	return event
}
