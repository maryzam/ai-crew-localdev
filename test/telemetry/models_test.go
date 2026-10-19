//go:build integration

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type sentinels struct {
	Prompt     string
	Response   string
	ToolInput  string
	ToolOutput string
}

func (s sentinels) byLabel() map[string]string {
	return map[string]string{"prompt": s.Prompt, "response": s.Response, "tool input": s.ToolInput, "tool output": s.ToolOutput}
}

func (s sentinels) userPrompt() string {
	return "Run the requested shell command, then reply. " + s.Prompt
}

func (s sentinels) toolCommand() string {
	suffix := strings.TrimPrefix(s.ToolOutput, "toolout-")
	return fmt.Sprintf("printf '%%s-%%s\\n' toolout %s # %s", suffix, s.ToolInput)
}

type modelLog struct {
	mu        sync.Mutex
	paths     []string
	toolUses  int
	finals    int
	hold      time.Duration
	finalSeen chan struct{}
	finalOnce sync.Once
}

func newModelLog(hold time.Duration) *modelLog {
	return &modelLog{hold: hold, finalSeen: make(chan struct{})}
}

func (l *modelLog) holdFinal(ctx context.Context) {
	l.finalOnce.Do(func() { close(l.finalSeen) })
	select {
	case <-time.After(l.hold):
	case <-ctx.Done():
	}
}

func (l *modelLog) record(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = append(l.paths, path)
}

func (l *modelLog) count(tool bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if tool {
		l.toolUses++
	} else {
		l.finals++
	}
}

func (l *modelLog) summary() (paths []string, toolUses, finals int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.paths...), l.toolUses, l.finals
}

type sseWriter struct {
	w http.ResponseWriter
}

func (s sseWriter) event(name string, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func startSSE(w http.ResponseWriter) sseWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	return sseWriter{w}
}

type anthropicModel struct {
	sentinels sentinels
	log       *modelLog
}

func (m *anthropicModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.log.record(r.Method + " " + r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages") {
		m.messages(w, r, body)
		return
	}
	http.NotFound(w, r)
}

func (m *anthropicModel) messages(w http.ResponseWriter, r *http.Request, body []byte) {
	var req struct {
		Model string `json:"model"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(body, &req)
	hasBash := false
	for _, tool := range req.Tools {
		hasBash = hasBash || tool.Name == "Bash"
	}
	var block map[string]any
	stop := "end_turn"
	switch {
	case bytes.Contains(body, []byte(`"tool_result"`)):
		m.log.holdFinal(r.Context())
		block = map[string]any{"type": "text", "text": m.sentinels.Response}
		m.log.count(false)
	case hasBash && bytes.Contains(body, []byte(m.sentinels.Prompt)):
		block = map[string]any{"type": "tool_use", "id": "toolu_spike01", "name": "Bash", "input": map[string]any{"command": m.sentinels.toolCommand(), "description": "Print spike marker"}}
		stop = "tool_use"
		m.log.count(true)
	default:
		block = map[string]any{"type": "text", "text": "ok"}
	}
	usage := map[string]any{"input_tokens": 12, "output_tokens": 6, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
	sse := startSSE(w)
	sse.event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_spike", "type": "message", "role": "assistant", "model": req.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage,
	}})
	if block["type"] == "tool_use" {
		input, _ := json.Marshal(block["input"])
		sse.event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}}})
		sse.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}})
	} else {
		sse.event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		sse.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": block["text"]}})
	}
	sse.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	sse.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 6}})
	sse.event("message_stop", map[string]any{"type": "message_stop"})
}

type responsesModel struct {
	sentinels sentinels
	log       *modelLog
}

func (m *responsesModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.log.record(r.Method + " " + r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/responses") {
		m.responses(w, r, body)
		return
	}
	http.NotFound(w, r)
}

func (m *responsesModel) responses(w http.ResponseWriter, r *http.Request, body []byte) {
	var item map[string]any
	switch {
	case bytes.Contains(body, []byte(`"custom_tool_call_output"`)):
		m.log.holdFinal(r.Context())
		item = map[string]any{"type": "message", "role": "assistant", "id": "msg_spike", "content": []any{map[string]any{"type": "output_text", "text": m.sentinels.Response, "annotations": []any{}}}}
		m.log.count(false)
	case bytes.Contains(body, []byte(m.sentinels.Prompt)):
		item = m.execCall()
		m.log.count(true)
	default:
		item = map[string]any{"type": "message", "role": "assistant", "id": "msg_ok", "content": []any{map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}}}}
	}
	sse := startSSE(w)
	sse.event("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_spike"}})
	sse.event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
	sse.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
	sse.event("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_spike", "usage": map[string]any{
		"input_tokens": 12, "input_tokens_details": map[string]any{"cached_tokens": 0},
		"output_tokens": 6, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": 18,
	}}})
}

func (m *responsesModel) execCall() map[string]any {
	command, _ := json.Marshal(m.sentinels.toolCommand())
	source := "const r = await tools.exec_command({cmd: " + string(command) + "});\ntext(typeof r === \"string\" ? r : JSON.stringify(r));\n"
	return map[string]any{"type": "custom_tool_call", "id": "ctc_spike", "call_id": "call_spike01", "name": "exec", "input": source}
}
