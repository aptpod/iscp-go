package nic_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/transport/nic"
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
		defer m.Close()
		assert.Equal(t, "eth1", m.GetCurrentNIC())
	})

	t.Run("without initial NIC", func(t *testing.T) {
		m := OpenManager([]string{"eth0", "eth1"}, "")
		defer m.Close()
		assert.Equal(t, "eth0", m.GetCurrentNIC())
	})
}

func TestNICManager_ChangeNIC(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

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

func TestNICManager_SubscribeAfterCloseDoesNotLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	m.Close()

	eventCh := m.Subscribe()
	select {
	case _, ok := <-eventCh:
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for closed subscription")
	}
}

func TestNICManager_ChangeNICAfterCloseFails(t *testing.T) {
	m := OpenManager([]string{"eth0", "eth1"}, "eth0")
	m.Close()

	assert.Error(t, m.ChangeNIC("eth1"))
}

func TestNICManager_GetNICNames(t *testing.T) {
	nics := []string{"eth0", "eth1"}
	m := OpenManager(nics, "eth0")
	defer m.Close()

	assert.Equal(t, nics, m.GetNICNames())
}
