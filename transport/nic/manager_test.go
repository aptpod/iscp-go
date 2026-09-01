package nic_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/transport/nic"
)

func TestNewNICManager(t *testing.T) {
	t.Run("with empty NIC names", func(t *testing.T) {
		assert.Panics(t, func() {
			OpenManager(nil, "")
		})
		assert.Panics(t, func() {
			OpenManager([]string{}, "")
		})
	})

	t.Run("with initial NIC", func(t *testing.T) {
		m := OpenManager([]string{"eth0", "eth1"}, "eth1")
		assert.Equal(t, "eth1", m.GetCurrentNIC())
	})

	t.Run("without initial NIC", func(t *testing.T) {
		m := OpenManager([]string{"eth0", "eth1"}, "")
		assert.Equal(t, "eth0", m.GetCurrentNIC())
	})
}

func TestNICManager_ChangeNIC(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")

	t.Run("successful change", func(t *testing.T) {
		assert.NoError(t, m.ChangeNIC("eth1"))
		time.Sleep(100 * time.Millisecond) // Allow time for the change to propagate
		assert.Equal(t, "eth1", m.GetCurrentNIC())
	})

	t.Run("buffer full", func(t *testing.T) {
		// Fill the buffer
		var err error
		for i := 0; i < 100; i++ {
			err = m.ChangeNIC("eth0")
			if err != nil {
				break
			}
		}
		// This should fail as the buffer is full
		assert.Error(t, err)
	})
}

func TestNICManager_Subscribe(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")

	t.Run("receive NIC changes", func(t *testing.T) {
		ch := ManagerSubscribe(m)
		assert.NoError(t, m.ChangeNIC("eth1"))

		select {
		case nic := <-ch:
			assert.Equal(t, "eth1", nic)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for NIC change")
		}

		ManagerUnubscribe(m, ch)
		assert.NoError(t, m.ChangeNIC("eth0"))

		select {
		case <-ch:
			t.Fatal("should not receive after unsubscribe")
		case <-time.After(100 * time.Millisecond):
			// Expected timeout
		}
	})
}

func TestNICManager_NewTransportSubscriber(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")

	t.Run("receive transport changes", func(t *testing.T) {
		eventCh := m.Subscribe()

		assert.NoError(t, m.ChangeNIC("eth1"))

		select {
		case transportID := <-eventCh:
			assert.Equal(t, "eth1", transportID)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for transport change")
		}
	})
}

func TestNICManager_CloseAfterChangeNICDoesNotPanic(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	_ = m.Subscribe()

	assert.NoError(t, m.ChangeNIC("eth1"))
	m.Close()
}

func TestNICManager_SubscribeAfterCloseDoesNotLeakGoroutine(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	m.Close()

	ch := m.Subscribe()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed, which means the Subscribe goroutine has exited")
	case <-time.After(time.Second):
		t.Fatal("timeout: Subscribe goroutine appears to be leaked")
	}
}

func TestNICManager_ChangeNICAfterCloseAlwaysReturnsError(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	m.Close()

	for i := 0; i < 100; i++ {
		assert.Error(t, m.ChangeNIC("eth1"), "iteration %d", i)
	}
}

func TestNICManager_CloseDoesNotWaitForBlockedSubscriberLog(t *testing.T) {
	oldLogger := slog.Default()
	handler := &blockingSlogHandler{
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
		finished: make(chan struct{}, 1),
	}
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(oldLogger)

	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	var closeOnce sync.Once
	closeManager := func() {
		closeOnce.Do(m.Close)
	}
	var releaseOnce sync.Once
	releaseLog := func() {
		releaseOnce.Do(func() { close(handler.release) })
	}
	t.Cleanup(func() {
		releaseLog()
		closeManager()
	})

	subscriber := ManagerSubscribe(m)
	subscriber <- "occupied"
	assert.NoError(t, m.ChangeNIC("eth1"))

	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the subscriber log handler")
	}

	closeDone := make(chan struct{})
	go func() {
		closeManager()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(time.Second):
		releaseLog()
		<-closeDone
		<-handler.finished
		t.Fatal("Close waited for the subscriber log handler")
	}

	releaseLog()
	select {
	case <-handler.finished:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for the subscriber log handler")
	}
}

type blockingSlogHandler struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (h *blockingSlogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *blockingSlogHandler) Handle(context.Context, slog.Record) error {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.release
	select {
	case h.finished <- struct{}{}:
	default:
	}
	return nil
}

func (h *blockingSlogHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *blockingSlogHandler) WithGroup(string) slog.Handler {
	return h
}

func TestNICManager_GetNICNames(t *testing.T) {
	nics := []string{"eth0", "eth1"}
	m := OpenManager(nics, "eth0")

	assert.Equal(t, nics, m.GetNICNames())
}
