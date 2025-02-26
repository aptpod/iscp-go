package nic

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
)

type Manager struct {
	nicNames         []string
	currentNICName   *atomic.Value
	subscribers      []chan string
	subscribersMu    sync.Mutex
	nicChangeEventCh chan string

	ctx    context.Context
	cancel context.CancelFunc
}

func OpenManager(nicNames []string, initialNIC string) *Manager {
	if len(nicNames) == 0 {
		panic("NICNames is required(ex eth0, eth1)")
	}

	var currentNIC atomic.Value
	currentNIC.Store(initialNIC)
	if currentNIC.Load() == "" {
		currentNIC.Store(nicNames[0])
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		nicNames:         nicNames,
		currentNICName:   &currentNIC,
		nicChangeEventCh: make(chan string, 8),

		ctx:    ctx,
		cancel: cancel,
	}
	go m.start()
	return m
}

func (m *Manager) Close() {
	select {
	case <-m.ctx.Done():
		return
	default:
	}
	m.cancel()
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	for _, ch := range m.subscribers {
		close(ch)
	}
}

func (m *Manager) GetCurrentNIC() string {
	return m.currentNICName.Load().(string)
}

func (m *Manager) GetNICNames() []string {
	return m.nicNames
}

func (m *Manager) ChangeNIC(nic string) error {
	select {
	case m.nicChangeEventCh <- nic:
		return nil
	case <-m.ctx.Done():
		return fmt.Errorf("already closed")
	default:
		return fmt.Errorf("failed to change NIC")
	}
}

func (m *Manager) subscribe() chan string {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	ch := make(chan string, 1)
	m.subscribers = append(m.subscribers, ch)
	return ch
}

func (m *Manager) unsubscribe(ch chan string) {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	m.subscribers = slices.DeleteFunc(m.subscribers, func(v chan string) bool {
		return v == ch
	})
}

func (m *Manager) Subscribe() <-chan string {
	ch := m.subscribe()
	resCh := make(chan string, 1)
	go func() {
		defer close(resCh)
		defer m.unsubscribe(ch)
		for nic := range ch {
			select {
			case <-m.ctx.Done():
				return
			case resCh <- nic:
			}
		}
	}()
	return resCh
}

func (m *Manager) start() error {
	for {
		select {
		case <-m.ctx.Done():
			return nil
		case nic := <-m.nicChangeEventCh:
			m.currentNICName.Store(nic)
			m.subscribersMu.Lock()
			subs := m.subscribers
			m.subscribersMu.Unlock()
			for _, ch := range subs {
				select {
				case <-m.ctx.Done():
				case ch <- nic:
				default:
					slog.WarnContext(m.ctx, "Failed to send NIC change event", "nic", nic)
				}
			}
		}
	}
}
