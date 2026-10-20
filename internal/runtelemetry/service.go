package runtelemetry

import (
	"context"
	"errors"
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
}

func NewService(root *evidence.Root, view View) *Service {
	return &Service{root: root, view: view, receivers: map[string]*serviceReceiver{}}
}

func (s *Service) Record(c Context, agent Agent, signal string, payload []byte) error {
	receiver, err := s.receiver(c)
	if err != nil {
		return err
	}
	return receiver.Store(agent, signal, payload)
}

func (s *Service) Close(run string) error {
	s.mu.Lock()
	entry := s.receivers[run]
	delete(s.receivers, run)
	s.mu.Unlock()
	if entry == nil {
		return nil
	}
	entry.cancel()
	statsErr := <-entry.done
	return errors.Join(statsErr, entry.receiver.Close())
}

func (s *Service) CloseAll() error {
	s.mu.Lock()
	runs := make([]string, 0, len(s.receivers))
	for run := range s.receivers {
		runs = append(runs, run)
	}
	s.mu.Unlock()
	var closeErr error
	for _, run := range runs {
		closeErr = errors.Join(closeErr, s.Close(run))
	}
	return closeErr
}

func (s *Service) receiver(c Context) (*Receiver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.receivers[c.RunID]; existing != nil {
		return existing.receiver, nil
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
	s.receivers[c.RunID] = &serviceReceiver{receiver: receiver, cancel: cancel, done: done}
	return receiver, nil
}
