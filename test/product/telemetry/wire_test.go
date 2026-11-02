package blackbox

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/product/wirecapture"
	"golang.org/x/sys/unix"
)

const wireSeeds = 5

type wireRun struct {
	root     *evidence.Root
	run      string
	recorder *wirecapture.Recorder
	front    *httptest.Server
}

func startWire(t *testing.T, limits wirecapture.Limits, upstream http.HandlerFunc) *wireRun {
	t.Helper()
	root := newRoot(t)
	return startWireOn(t, root, "run-wire", limits, upstream)
}

func startWireOn(t *testing.T, root *evidence.Root, run string, limits wirecapture.Limits, upstream http.HandlerFunc) *wireRun {
	t.Helper()
	backend := httptest.NewServer(upstream)
	t.Cleanup(backend.Close)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := wirecapture.Open(root, run, wirecapture.Settings{Enabled: true}, limits)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(target) }, ErrorLog: nil}
	front := httptest.NewServer(recorder.Middleware("claude", proxy))
	t.Cleanup(front.Close)
	return &wireRun{root: root, run: run, recorder: recorder, front: front}
}

type capturedExchange struct {
	exchange wirecapture.Exchange
	request  []byte
	response []byte
}

func (w *wireRun) captured(t *testing.T) []capturedExchange {
	t.Helper()
	if err := w.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var all []capturedExchange
	if err := wirecapture.Each(w.root, w.run, func(exchange wirecapture.Exchange, request, response []byte) error {
		all = append(all, capturedExchange{exchange, request, response})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return all
}

func (w *wireRun) post(t *testing.T, body []byte) string {
	t.Helper()
	response, err := http.Post(w.front.URL+"/v1/messages?beta=true", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func waitForStored(t *testing.T, w *wireRun, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := wirecapture.Summarize(w.root, w.run)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Exchanges >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d exchanges were not stored in time", want)
}

func randomText(t *testing.T, size int) string {
	t.Helper()
	data := make([]byte, size/2+1)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(data)[:size]
}

func echoUpstream(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_, _ = fmt.Fprintf(w, "received %d bytes", len(body))
}

func TestCapturedTrafficIsExactAndStreamsThroughUnbuffered(t *testing.T) {
	release := make(chan struct{})
	var received []byte
	w := startWire(t, wirecapture.DefaultLimits, func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "req_123")
		w.Header().Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.42")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "event: last\n\n")
	})
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` + randomText(t, 200_000) + `"}],"system":"sys","tools":[]}`)
	request, err := http.NewRequest(http.MethodPost, w.front.URL+"/v1/messages?secret=query", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-Request-Id", "client-1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	firstLine := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		firstLine <- line
	}()
	select {
	case line := <-firstLine:
		if line != "event: first\n" {
			t.Fatalf("first line %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture buffered the stream: the first event never reached the client")
	}
	close(release)
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	all := w.captured(t)
	if len(all) != 1 {
		t.Fatalf("captured %d exchanges", len(all))
	}
	got := all[0]
	if !bytes.Equal(got.request, body) || !bytes.Equal(got.request, received) {
		t.Errorf("captured request differs from the wire: %d bytes against %d sent", len(got.request), len(body))
	}
	if string(got.response) != "event: first\n\nevent: last\n\n" {
		t.Errorf("captured response %q", got.response)
	}
	if !got.exchange.Request.Complete || !got.exchange.Response.Complete || got.exchange.Status != http.StatusOK {
		t.Errorf("exchange %+v", got.exchange)
	}
	if got.exchange.ResponseHeaders["Request-Id"][0] != "req_123" || got.exchange.ResponseHeaders["Anthropic-Ratelimit-Unified-5h-Utilization"][0] != "0.42" || got.exchange.RequestHeaders["X-Client-Request-Id"][0] != "client-1" {
		t.Errorf("correlation and quota headers were not kept: %+v %+v", got.exchange.RequestHeaders, got.exchange.ResponseHeaders)
	}
	if got.exchange.Path != "/v1/messages" {
		t.Errorf("path %q kept more than the route", got.exchange.Path)
	}
}

func seededText(generator *mathrand.Rand, size int) string {
	data := make([]byte, size/2+1)
	for index := range data {
		data[index] = byte(generator.IntN(256))
	}
	return hex.EncodeToString(data)[:size]
}

func TestGrowingConversationHistoryIsStoredOnce(t *testing.T) {
	const turns, budgetPercent = 120, 10
	for seed := range uint64(wireSeeds) {
		w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
		generator := mathrand.New(mathrand.NewPCG(seed, seed))
		suffix := `],"system":"` + seededText(generator, 30_000) + `","tools":"` + seededText(generator, 80_000) + `"}`
		history := ""
		sent := map[string]bool{}
		var wireBytes int
		for turn := range turns {
			history += fmt.Sprintf(`{"role":"user","content":"%s"},`, seededText(generator, 4_000))
			body := `{"model":"m","messages":[` + history + fmt.Sprintf(`{"role":"user","content":"turn %d"}`, turn) + suffix
			sent[body] = true
			wireBytes += len(body)
			w.post(t, []byte(body))
		}
		all := w.captured(t)
		if len(all) != turns {
			t.Fatalf("seed %d: captured %d of %d exchanges", seed, len(all), turns)
		}
		for _, exchange := range all {
			if !sent[string(exchange.request)] {
				t.Fatalf("seed %d: exchange %s did not round trip exactly", seed, exchange.exchange.ID)
			}
		}
		summary, err := wirecapture.Summarize(w.root, w.run)
		if err != nil {
			t.Fatal(err)
		}
		share := 100 * float64(summary.StoredBytes) / float64(wireBytes)
		t.Logf("seed %d: stored %d bytes of %d on the wire (%.1f%%)", seed, summary.StoredBytes, wireBytes, share)
		if share > budgetPercent {
			t.Errorf("seed %d: stored %.1f%% of the wire bytes, over the %d%% budget", seed, share, budgetPercent)
		}
	}
}

func TestCredentialsNeverReachCapturedEvidence(t *testing.T) {
	const placeholder, apiKey, cookie, hostSecret, setCookie = "placeholder-sentinel-0123456789", "api-key-sentinel-0123456789", "cookie-sentinel-0123456789", "host-secret-sentinel-0123456789", "set-cookie-sentinel-0123456789"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+hostSecret {
			t.Errorf("upstream did not receive the host credential")
		}
		w.Header().Set("Set-Cookie", "s="+setCookie)
		_, _ = io.WriteString(w, "echo "+hostSecret)
	}))
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	swapping := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Header.Set("Authorization", "Bearer "+hostSecret)
		},
		ModifyResponse: func(resp *http.Response) error {
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			resp.Header.Del("Set-Cookie")
			resp.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(data), hostSecret, "[redacted]")))
			resp.ContentLength = -1
			resp.Header.Del("Content-Length")
			return nil
		},
	}
	root := newRoot(t)
	recorder, err := wirecapture.Open(root, "run-secrets", wirecapture.Settings{Enabled: true}, wirecapture.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(recorder.Middleware("claude", swapping))
	t.Cleanup(front.Close)
	request, err := http.NewRequest(http.MethodPost, front.URL+"/v1/messages", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+placeholder)
	request.Header.Set("X-Api-Key", apiKey)
	request.Header.Set("Cookie", "c="+cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := root.RunDir("run-secrets")
	if err != nil {
		t.Fatal(err)
	}
	stored := 0
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		stored++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range []string{placeholder, apiKey, cookie, hostSecret, setCookie} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s holds credential sentinel %s", path, secret)
			}
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode %v is not owner-only", path, info.Mode().Perm())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stored < 3 {
		t.Fatalf("only %d evidence files were inspected", stored)
	}
}

func TestOversizedBodyIsForwardedWholeAndMarkedIncomplete(t *testing.T) {
	var received int
	w := startWire(t, wirecapture.Limits{MaxBodyBytes: 1 << 20, MaxInFlightBytes: 16 << 20}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = len(body)
		_, _ = io.WriteString(w, "ok")
	})
	body := []byte(randomText(t, 3<<20))
	if got := w.post(t, body); got != "ok" {
		t.Fatalf("response %q", got)
	}
	if received != len(body) {
		t.Fatalf("upstream received %d of %d bytes", received, len(body))
	}
	all := w.captured(t)
	if len(all) != 1 || all[0].exchange.Request.Complete || all[0].exchange.Request.Bytes != int64(len(body)) || int64(len(all[0].request)) > 1<<20 || !bytes.HasPrefix(body, all[0].request) {
		t.Fatalf("oversized request record: %+v", all[0].exchange.Request)
	}
	summary, err := wirecapture.Summarize(w.root, w.run)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Incomplete != 1 {
		t.Fatalf("summary %+v", summary)
	}
}

func TestInFlightBudgetLimitsCaptureNotTraffic(t *testing.T) {
	var arrived sync.WaitGroup
	arrived.Add(2)
	w := startWire(t, wirecapture.Limits{MaxBodyBytes: 1 << 20, MaxInFlightBytes: 1 << 20}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		arrived.Done()
		arrived.Wait()
		_, _ = io.WriteString(w, "ok")
	})
	var clients sync.WaitGroup
	for range 2 {
		clients.Add(1)
		go func() {
			defer clients.Done()
			if got := w.post(t, []byte(randomText(t, 800<<10))); got != "ok" {
				t.Errorf("response %q", got)
			}
		}()
	}
	clients.Wait()
	incomplete, stored := 0, 0
	for _, exchange := range w.captured(t) {
		stored += len(exchange.request)
		if !exchange.exchange.Request.Complete {
			incomplete++
		}
	}
	if incomplete == 0 || stored > 1<<20 {
		t.Fatalf("%d requests cut with %d bytes held against a %d byte in-flight budget", incomplete, stored, 1<<20)
	}
}

func TestAbortedStreamKeepsWhatWasDeliveredAndStillAborts(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "partial event")
		w.(http.Flusher).Flush()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	})
	response, err := http.Post(w.front.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr == nil {
		t.Fatal("the client was not told the stream aborted")
	}
	all := w.captured(t)
	if len(all) != 1 || all[0].exchange.Response.Complete || string(all[0].response) != "partial event" {
		t.Fatalf("aborted exchange: %+v %q", all[0].exchange.Response, all[0].response)
	}
}

func TestStuckStorageNeverDelaysModelTrafficAndTheLossIsRecorded(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
	dir, err := w.root.RunDir(w.run)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if got := w.post(t, []byte("{}")); !strings.HasPrefix(got, "received") {
		t.Fatalf("response %q", got)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stuck storage delayed the response by %s", elapsed)
	}
	if err := w.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	summary, err := wirecapture.Summarize(w.root, w.run)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failed != 1 || summary.Exchanges != 0 {
		t.Fatalf("lost exchange is not visible: %+v", summary)
	}
}

func TestPrunedWireIsRecordedAndCaptureRecovers(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
	body := []byte(randomText(t, 100_000))
	w.post(t, body)
	waitForStored(t, w, 1)
	pruned, err := w.root.PruneTelemetry(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0].File != evidence.WireDir || pruned[0].Bytes < int64(len(body)) {
		t.Fatalf("pruned %+v", pruned)
	}
	w.post(t, body)
	all := w.captured(t)
	if len(all) != 1 || !bytes.Equal(all[0].request, body) {
		t.Fatalf("capture after pruning did not recover: %d exchanges", len(all))
	}
}

func TestActiveRunsKeepTheirWireWhenPruning(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
	w.post(t, []byte(randomText(t, 10_000)))
	if err := w.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.root.PruneTelemetry(0, map[string]bool{w.run: true}); err == nil {
		t.Fatal("an active run over budget did not report the exceeded budget")
	}
	if all := w.captured(t); len(all) != 1 {
		t.Fatalf("active run wire lost: %d exchanges", len(all))
	}
}

func TestDamagedChunksAreDetectedNotReturned(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
	w.post(t, []byte(randomText(t, 10_000)))
	if err := w.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := w.root.RunDir(w.run)
	if err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(dir, evidence.WireDir, "chunks.bin")
	data, err := os.ReadFile(pack)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(pack, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = wirecapture.Each(w.root, w.run, func(wirecapture.Exchange, []byte, []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("damaged chunks returned %v", err)
	}
}

func TestCaptureSurvivesARestartAndATornRecord(t *testing.T) {
	root := newRoot(t)
	first := startWireOn(t, root, "run-restart", wirecapture.DefaultLimits, echoUpstream)
	before := []byte(randomText(t, 50_000))
	first.post(t, before)
	if err := first.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := root.RunDir("run-restart")
	if err != nil {
		t.Fatal(err)
	}
	for name, torn := range map[string]string{"chunks.bin": "torn chunk bytes", "exchanges.jsonl": `{"id":"torn`} {
		file, err := os.OpenFile(filepath.Join(dir, evidence.WireDir, name), os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(torn); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	second := startWireOn(t, root, "run-restart", wirecapture.DefaultLimits, echoUpstream)
	after := []byte(randomText(t, 50_000))
	second.post(t, after)
	if err := second.recorder.Close(); err != nil {
		t.Fatal(err)
	}
	var all [][]byte
	err = wirecapture.Each(root, "run-restart", func(_ wirecapture.Exchange, request, _ []byte) error {
		all = append(all, request)
		return nil
	})
	if !errors.Is(err, wirecapture.ErrUnreadableRecords) {
		t.Fatalf("the torn record was not reported: %v", err)
	}
	if len(all) != 2 || !bytes.Equal(all[0], before) || !bytes.Equal(all[1], after) {
		t.Fatalf("restart lost traffic: %d exchanges", len(all))
	}
	summary, err := wirecapture.Summarize(root, "run-restart")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Malformed != 1 || summary.Exchanges != 2 {
		t.Fatalf("torn record is not visible: %+v", summary)
	}
}

func TestConcurrentExchangesAllRoundTrip(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
	shared := randomText(t, 200_000)
	const clients, each = 8, 10
	sent := make(chan string, clients*each)
	var group sync.WaitGroup
	for client := range clients {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range each {
				body := fmt.Sprintf("%s client %d request %d", shared, client, index)
				w.post(t, []byte(body))
				sent <- body
			}
		}()
	}
	group.Wait()
	close(sent)
	want := map[string]bool{}
	for body := range sent {
		want[body] = true
	}
	all := w.captured(t)
	if len(all) != clients*each {
		t.Fatalf("captured %d exchanges", len(all))
	}
	for _, exchange := range all {
		if !want[string(exchange.request)] {
			t.Fatalf("exchange %s did not round trip", exchange.exchange.ID)
		}
	}
}

func TestUpgradedConnectionsAreRecordedAsNotCaptured(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	request, err := http.NewRequest(http.MethodGet, w.front.URL+"/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade did not pass through: %d", response.StatusCode)
	}
	_ = response.Body.Close()
	all := w.captured(t)
	if len(all) != 1 || all[0].exchange.Status != http.StatusSwitchingProtocols || all[0].exchange.Response.Complete {
		t.Fatalf("upgrade record: %+v", all[0].exchange)
	}
}

func TestHandlerThatWritesNothingRecordsTheImplicitStatus(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, func(http.ResponseWriter, *http.Request) {})
	w.post(t, []byte("{}"))
	all := w.captured(t)
	if len(all) != 1 || all[0].exchange.Status != http.StatusOK {
		t.Fatalf("status %+v", all[0].exchange)
	}
}

func TestShutdownCountsExchangesItCouldNotFinishAndLateRefusals(t *testing.T) {
	w := startWire(t, wirecapture.DefaultLimits, echoUpstream)
	dir, err := w.root.RunDir(w.run)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	const sent = 8
	for range sent {
		w.post(t, []byte("{}"))
	}
	if err := w.recorder.Close(); err == nil {
		t.Fatal("a shutdown that could not drain reported success")
	}
	w.post(t, []byte("{}"))
	summary, err := wirecapture.Summarize(w.root, w.run)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failed < sent {
		t.Fatalf("%d exchanges were lost, but only %d are visible", sent+1, summary.Failed)
	}
}

func TestDisabledCaptureStoresNothingAndRecordsThatItWasOff(t *testing.T) {
	root := newRoot(t)
	recorder, err := wirecapture.Open(root, "run-off", wirecapture.Settings{Enabled: false}, wirecapture.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(recorder.Middleware("claude", http.HandlerFunc(echoUpstream)))
	t.Cleanup(front.Close)
	response, err := http.Post(front.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := root.RunDir("run-off")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, evidence.WireDir)); !os.IsNotExist(err) {
		t.Fatalf("capture wrote while off: %v", err)
	}
	summary, err := wirecapture.Summarize(root, "run-off")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Enabled == nil || *summary.Enabled || summary.Exchanges != 0 {
		t.Fatalf("summary %+v", summary)
	}
}
