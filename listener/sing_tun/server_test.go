package sing_tun

import (
	"context"
	"errors"
	"testing"
)

type lifecycleStack struct {
	ctx         context.Context
	startErr    error
	closeErr    error
	starts      int
	closes      int
	closeCtxErr error
}

func (s *lifecycleStack) Start() error {
	s.starts++
	return s.startErr
}

func (s *lifecycleStack) Close() error {
	s.closes++
	s.closeCtxErr = s.ctx.Err()
	return s.closeErr
}

func TestStackStartFailureCleansResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startErr := errors.New("injected start failure")
	stack := &lifecycleStack{ctx: ctx, startErr: startErr, closeErr: errors.New("injected close failure")}
	l := &Listener{cancel: cancel}
	if err := l.startStack(stack); err != startErr {
		t.Fatalf("startStack error = %v, want original start error", err)
	}
	if stack.starts != 1 || stack.closes != 1 || stack.closeCtxErr != context.Canceled {
		t.Fatalf("failed stack: starts=%d closes=%d context=%v", stack.starts, stack.closes, stack.closeCtxErr)
	}
	if l.tunStack != nil {
		t.Fatal("failed stack retained by listener")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if stack.closes != 1 {
		t.Fatalf("failed stack closed %d times", stack.closes)
	}
}

func TestStackStartSuccessOwnsUntilClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stack := &lifecycleStack{ctx: ctx}
	l := &Listener{cancel: cancel}
	if err := l.startStack(stack); err != nil {
		t.Fatal(err)
	}
	if l.tunStack != stack || stack.starts != 1 || stack.closes != 0 || ctx.Err() != nil {
		t.Fatalf("live stack: ownership=%v starts=%d closes=%d context=%v", l.tunStack == stack, stack.starts, stack.closes, ctx.Err())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if stack.closes != 1 || stack.closeCtxErr != context.Canceled {
		t.Fatalf("closed stack: closes=%d context=%v", stack.closes, stack.closeCtxErr)
	}
}

func TestListenerCloseCancelsBeforeStackOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := &Listener{cancel: cancel}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("listener context = %v", ctx.Err())
	}
}
