package wirecapture

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
)

const (
	CaptureEvent = "wire.capture"
	drainBudget  = 10 * time.Second
)

type Limits struct {
	MaxBodyBytes     int64
	MaxInFlightBytes int64
}

var DefaultLimits = Limits{MaxBodyBytes: 64 << 20, MaxInFlightBytes: 256 << 20}

var requestHeaders = map[string]bool{
	"Anthropic-Beta": true, "Anthropic-Version": true, "Content-Encoding": true, "Content-Type": true, "Openai-Beta": true, "Originator": true, "Session_id": true, "User-Agent": true, "X-Claude-Code-Session-Id": true, "X-Client-Request-Id": true,
}

var responseHeaders = map[string]bool{
	"Content-Encoding": true, "Content-Type": true, "Request-Id": true, "Retry-After": true, "X-Request-Id": true, "X-Should-Retry": true,
}

var responseHeaderPrefixes = []string{"Anthropic-Ratelimit-", "X-Codex-", "X-Ratelimit-"}

type Stats struct {
	Stored uint64 `json:"stored"`
	Failed uint64 `json:"failed"`
}

type snapshot struct {
	Instance   string `json:"instance"`
	Enabled    bool   `json:"enabled"`
	Unfinished int64  `json:"unfinished,omitempty"`
	Stats
}

type Recorder struct {
	root     *evidence.Root
	run      string
	enabled  bool
	limits   Limits
	store    *store
	instance string
	inFlight atomic.Int64
	stored   atomic.Uint64
	failed   atomic.Uint64
	reported atomic.Uint64
	open     atomic.Int64
	gate     sync.Mutex
	closed   bool
	pending  sync.WaitGroup
}

func Open(root *evidence.Root, run string, settings Settings, limits Limits) (*Recorder, error) {
	if root == nil {
		return nil, errors.New("wire capture needs an evidence root")
	}
	if err := evidence.ValidateRun(run); err != nil {
		return nil, err
	}
	if limits.MaxBodyBytes <= 0 || limits.MaxInFlightBytes <= 0 {
		return nil, errors.New("wire capture limits must be positive")
	}
	instance, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("create wire capture instance ID: %w", err)
	}
	recorder := &Recorder{root: root, run: run, enabled: settings.Enabled, limits: limits, store: newStore(root, run), instance: instance}
	if err := recorder.report(); err != nil {
		return nil, fmt.Errorf("record wire capture state: %w", err)
	}
	return recorder, nil
}

func (r *Recorder) Stats() Stats {
	return Stats{Stored: r.stored.Load(), Failed: r.failed.Load()}
}

func (r *Recorder) report() error {
	stats := r.Stats()
	if err := r.root.AppendRunEvent(r.run, CaptureEvent, snapshot{Instance: r.instance, Enabled: r.enabled, Unfinished: r.open.Load(), Stats: stats}); err != nil {
		return err
	}
	r.reported.Store(stats.Failed)
	return nil
}

func (r *Recorder) admit() bool {
	r.gate.Lock()
	defer r.gate.Unlock()
	if r.closed {
		r.failed.Add(1)
		return false
	}
	r.pending.Add(1)
	r.open.Add(1)
	return true
}

func (r *Recorder) Close() error {
	r.gate.Lock()
	r.closed = true
	r.gate.Unlock()
	drained := make(chan struct{})
	go func() {
		r.pending.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(drainBudget):
		return errors.Join(fmt.Errorf("wire capture did not drain within %s", drainBudget), r.report())
	}
	return r.report()
}

func (r *Recorder) Middleware(agent string, next http.Handler) http.Handler {
	if !r.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !r.admit() {
			next.ServeHTTP(w, req)
			_ = r.report()
			return
		}
		exchange := newExchange(agent, req)
		request := &tap{recorder: r, wholeWhenEmpty: req.ContentLength == 0}
		req.Body = &requestTap{ReadCloser: req.Body, tap: request}
		response := &responseTap{ResponseWriter: w, tap: tap{recorder: r}, started: exchange.Started}
		defer func() {
			failure := recover()
			r.finish(req, exchange, request, response)
			if failure != nil {
				panic(failure)
			}
		}()
		next.ServeHTTP(response, req)
		response.tap.mark(true)
	})
}

func newExchange(agent string, req *http.Request) *Exchange {
	id, err := randomID()
	if err != nil {
		id = fmt.Sprintf("time-%d", time.Now().UnixNano())
	}
	return &Exchange{ID: id, Agent: agent, Method: req.Method, Path: req.URL.Path, Started: time.Now().UTC(), RequestHeaders: selectHeaders(req.Header, requestHeaders, nil)}
}

func (r *Recorder) finish(req *http.Request, exchange *Exchange, request *tap, response *responseTap) {
	exchange.DurationMS = time.Since(exchange.Started).Milliseconds()
	exchange.FirstByteMS = response.firstByte.Milliseconds()
	exchange.Status = response.finalStatus()
	exchange.ResponseHeaders = response.headers
	exchange.Interrupted = req.Context().Err() != nil
	requestBody, requestSummary := request.seal()
	responseBody, responseSummary := response.tap.seal()
	responseSummary.Complete = responseSummary.Complete && !response.hijacked
	exchange.Request, exchange.Response = requestSummary, responseSummary
	go func() {
		defer r.pending.Done()
		defer r.open.Add(-1)
		defer r.inFlight.Add(-int64(len(requestBody) + len(responseBody)))
		if err := r.store.write(exchange, requestBody, responseBody); err != nil {
			r.failed.Add(1)
		} else {
			r.stored.Add(1)
		}
		if r.failed.Load() != r.reported.Load() {
			_ = r.report()
		}
	}()
}

type tap struct {
	recorder       *Recorder
	wholeWhenEmpty bool
	mu             sync.Mutex
	data           []byte
	total          int64
	truncated      bool
	sealed         bool
	ended          bool
}

func (t *tap) add(chunk []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.total += int64(len(chunk))
	if t.sealed || t.truncated || len(chunk) == 0 {
		return
	}
	size := int64(len(chunk))
	if int64(len(t.data))+size > t.recorder.limits.MaxBodyBytes || t.recorder.inFlight.Add(size) > t.recorder.limits.MaxInFlightBytes {
		if int64(len(t.data))+size <= t.recorder.limits.MaxBodyBytes {
			t.recorder.inFlight.Add(-size)
		}
		t.truncated = true
		return
	}
	t.data = append(t.data, chunk...)
}

func (t *tap) mark(ended bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ended = t.ended || ended
}

func (t *tap) seal() ([]byte, Body) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sealed = true
	complete := !t.truncated && (t.ended || t.wholeWhenEmpty)
	return t.data, Body{Bytes: t.total, Complete: complete}
}

type requestTap struct {
	io.ReadCloser
	tap *tap
}

func (r *requestTap) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.tap.add(p[:n])
	if errors.Is(err, io.EOF) {
		r.tap.mark(true)
	}
	return n, err
}

type responseTap struct {
	http.ResponseWriter
	tap       tap
	started   time.Time
	firstByte time.Duration
	status    int
	hijacked  bool
	headers   map[string][]string
}

func (r *responseTap) finalStatus() int {
	switch {
	case r.status != 0:
		return r.status
	case r.hijacked:
		return http.StatusSwitchingProtocols
	default:
		return http.StatusOK
	}
}

func (r *responseTap) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.hijacked = true
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

func (r *responseTap) settle(code int) {
	if r.status != 0 {
		return
	}
	r.status = code
	r.firstByte = time.Since(r.started)
	r.headers = selectHeaders(r.Header(), responseHeaders, responseHeaderPrefixes)
}

func (r *responseTap) WriteHeader(code int) {
	if code >= http.StatusOK || code == http.StatusSwitchingProtocols {
		r.settle(code)
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseTap) Write(p []byte) (int, error) {
	r.settle(http.StatusOK)
	n, err := r.ResponseWriter.Write(p)
	r.tap.add(p[:n])
	return n, err
}

func (r *responseTap) Flush() {
	r.settle(http.StatusOK)
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *responseTap) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func selectHeaders(headers http.Header, names map[string]bool, prefixes []string) map[string][]string {
	selected := map[string][]string{}
	for name, values := range headers {
		if names[name] || hasAnyPrefix(name, prefixes) {
			selected[name] = append([]string(nil), values...)
		}
	}
	return selected
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
