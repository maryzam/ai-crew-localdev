package runtelemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

const serviceStatsInterval = time.Second

type Service struct {
	root      *evidence.Root
	view      View
	mu        sync.Mutex
	receivers map[string]*serviceReceiver
}

type serviceReceiver struct {
	receiver *Receiver
	cancel   context.CancelFunc
	done     chan error
	inflight sync.WaitGroup
	closing  bool
	closed   chan struct{}
	closeErr error
}

func NewService(root *evidence.Root, view View) *Service {
	return &Service{root: root, view: view, receivers: map[string]*serviceReceiver{}}
}

func (s *Service) Record(c Context, agent Agent, signal string, payload []byte) error {
	entry, err := s.acquire(c)
	if err != nil {
		return err
	}
	defer entry.inflight.Done()
	return entry.receiver.Store(agent, signal, payload)
}

func (s *Service) RecordRelayLoss(c Context, rejected, failed uint64) error {
	entry, err := s.acquire(c)
	if err != nil {
		return err
	}
	defer entry.inflight.Done()
	entry.receiver.RecordRelayLoss(rejected, failed)
	return nil
}

func (s *Service) Close(run string) error {
	s.mu.Lock()
	entry := s.receivers[run]
	if entry == nil {
		s.mu.Unlock()
		return nil
	}
	if entry.closing {
		closed := entry.closed
		s.mu.Unlock()
		<-closed
		return entry.closeErr
	}
	entry.closing = true
	s.mu.Unlock()
	entry.inflight.Wait()
	entry.cancel()
	statsErr := <-entry.done
	entry.closeErr = errors.Join(statsErr, entry.receiver.Close())
	close(entry.closed)
	return entry.closeErr
}

func (s *Service) CloseAll() error {
	s.mu.Lock()
	runs := make([]string, 0, len(s.receivers))
	for run, entry := range s.receivers {
		if !entry.closing {
			runs = append(runs, run)
		}
	}
	s.mu.Unlock()
	var closeErr error
	for _, run := range runs {
		closeErr = errors.Join(closeErr, s.Close(run))
	}
	return closeErr
}

func (s *Service) acquire(c Context) (*serviceReceiver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.receivers[c.RunID]; existing != nil {
		if existing.closing {
			return nil, fmt.Errorf("telemetry run %q is closed", c.RunID)
		}
		existing.inflight.Add(1)
		return existing, nil
	}
	options := []ReceiverOption{}
	if s.view != nil {
		options = append(options, WithView(s.view))
	}
	receiver, err := NewReceiver(c, s.root, options...)
	if err != nil {
		return nil, err
	}
	statsContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- receiver.RecordStats(statsContext, serviceStatsInterval)
	}()
	entry := &serviceReceiver{receiver: receiver, cancel: cancel, done: done, closed: make(chan struct{})}
	entry.inflight.Add(1)
	s.receivers[c.RunID] = entry
	return entry, nil
}
