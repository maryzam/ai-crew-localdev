package uphost

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestBrokerWaitHonorsContextCancellation(t *testing.T) {
	supervisor := newBrokerSupervisor("missing", io.Discard, nil)
	supervisor.Timeout = time.Minute
	supervisor.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unavailable") }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if supervisor.wait(ctx) {
		t.Fatal("cancelled wait reported ready")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("cancelled wait did not return promptly")
	}
}
