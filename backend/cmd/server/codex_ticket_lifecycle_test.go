package main

import (
	"context"
	"errors"
	"testing"
)

type ticketShutdownStub struct {
	err     error
	called  bool
	bounded bool
}

func (s *ticketShutdownStub) Shutdown(ctx context.Context) error {
	s.called = true
	_, s.bounded = ctx.Deadline()
	return s.err
}
func TestCodexTicketShutdownRequiresSuccessfulJoin(t *testing.T) {
	for _, err := range []error{nil, context.DeadlineExceeded, errors.New("not joined")} {
		s := &ticketShutdownStub{err: err}
		got := shutdownCodexTicketBeforeDependencies(s)
		if got != (err == nil) || !s.called || !s.bounded {
			t.Fatalf("join result %v, called=%v bounded=%v for %v", got, s.called, s.bounded, err)
		}
	}
}
