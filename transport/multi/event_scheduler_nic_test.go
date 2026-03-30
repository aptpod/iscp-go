package multi_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/multi"
)

type mockNICEventSubscriber struct {
	ch chan string
}

func newMockNICEventSubscriber() *mockNICEventSubscriber {
	return &mockNICEventSubscriber{
		ch: make(chan string, 1),
	}
}

func (m *mockNICEventSubscriber) Subscribe() <-chan string {
	return m.ch
}

func TestNICEventScheduler(t *testing.T) {
	tests := []struct {
		name     string
		nicID    string
		wantTID  transport.SubConnectionID
		canceled bool
	}{
		{
			name:    "Success: NIC event is correctly converted to SubConnectionID",
			nicID:   "eth0",
			wantTID: transport.SubConnectionID("transport1"),
		},
		{
			name:     "Cancel: Context is canceled",
			nicID:    "eth0",
			wantTID:  transport.SubConnectionID("transport1"),
			canceled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// モックの準備
			mockNIC := newMockNICEventSubscriber()
			scheduler := &multi.NICEventSubscriber{
				NICManager: mockNIC,
				NICSubConnectionID: map[string]transport.SubConnectionID{
					tt.nicID: tt.wantTID,
				},
			}

			// コンテキストの準備
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Subscribeの開始
			ch := scheduler.Subscribe(ctx)

			if tt.canceled {
				// キャンセルケースのテスト
				cancel()
				time.Sleep(10 * time.Millisecond) // ゴルーチンの終了を待つ
				select {
				case _, ok := <-ch:
					assert.False(t, ok, "channel should be closed")
				default:
					t.Error("channel is not closed")
				}
				return
			}

			// NICイベントの送信
			mockNIC.ch <- tt.nicID

			// 結果の検証
			select {
			case got := <-ch:
				assert.Equal(t, tt.wantTID, got)
			case <-time.After(100 * time.Millisecond):
				t.Error("timeout: failed to receive SubConnectionID")
			}
		})
	}
}

func TestNICEventScheduler_UnknownNIC(t *testing.T) {
	mockNIC := newMockNICEventSubscriber()
	scheduler := &multi.NICEventSubscriber{
		NICManager:         mockNIC,
		NICSubConnectionID: map[string]transport.SubConnectionID{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := scheduler.Subscribe(ctx)

	// 未知のNICイベントを送信
	mockNIC.ch <- "unknown_nic"

	// 対応するSubConnectionIDがない場合はスキップされ、チャネルに何も送信されないことを確認
	select {
	case tid := <-ch:
		t.Errorf("unexpected SubConnectionID received for unknown NIC: %q", tid)
	case <-time.After(100 * time.Millisecond):
		// 期待通り: 未知NICはスキップされる
	}
}
