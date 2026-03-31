package iscp

import (
	"context"
	"sync"

	uuid "github.com/google/uuid"
)

// upstreamRepositoryは、アップストリーム情報のリポジトリインターフェースです。
type upstreamRepository interface {
	// SaveUpstreamは、アップストリーム情報を保存します。
	SaveUpstream(ctx context.Context, id uuid.UUID, info UpstreamState) (*UpstreamState, error)

	// FindUpstreamByIDは、指定したIDのアップストリーム情報を取得します。
	FindUpstreamByID(ctx context.Context, id uuid.UUID) (*UpstreamState, error)

	// RemoveUpstreamByIDは、指定したIDのアップストリーム情報を削除します。
	RemoveUpstreamByID(ctx context.Context, id uuid.UUID) error
}

// downstreamRepositoryは、ダウンストリーム情報のリポジトリインターフェースです。
type downstreamRepository interface {
	// SaveDownstreamは、ダウンストリーム情報を保存します。
	SaveDownstream(ctx context.Context, id uuid.UUID, info DownstreamState) (*DownstreamState, error)

	// FindDownstreamByIDは、指定したIDのダウンストリーム情報を取得します。
	FindDownstreamByID(ctx context.Context, id uuid.UUID) (*DownstreamState, error)

	// RemoveDownstreamByIDは、指定したIDのダウンストリーム情報を削除します。
	RemoveDownstreamByID(ctx context.Context, id uuid.UUID) error
}

// inmemStreamRepositoryは、ストリーム情報リポジトリのインメモリ実装です。
type inmemStreamRepository struct {
	sync.RWMutex
	upstream   map[uuid.UUID]*UpstreamState
	downstream map[uuid.UUID]*DownstreamState
}

// newInmemStreamRepositoryは、ストリーム情報リポジトリのインメモリ実装を生成します。
func newInmemStreamRepository() *inmemStreamRepository {
	return &inmemStreamRepository{
		upstream:   make(map[uuid.UUID]*UpstreamState),
		downstream: make(map[uuid.UUID]*DownstreamState),
	}
}

// SaveUpstreamはメモリ内にストリームを保存します。
func (r *inmemStreamRepository) SaveUpstream(ctx context.Context, id uuid.UUID, info UpstreamState) (*UpstreamState, error) {
	r.Lock()
	defer r.Unlock()

	r.upstream[id] = &info
	return &info, nil
}

// FindUpstreamByIDはメモリ内に保存されたストリームから、引数idに合致するストリームを返します。
func (r *inmemStreamRepository) FindUpstreamByID(ctx context.Context, id uuid.UUID) (*UpstreamState, error) {
	r.RLock()
	defer r.RUnlock()

	res, ok := r.upstream[id]
	if !ok {
		return nil, ErrStreamNotFound
	}
	return res, nil
}

// RemoveUpstreamByIDはメモリ内に保存されたストリームから、引数idのストリームを削除します。
func (r *inmemStreamRepository) RemoveUpstreamByID(ctx context.Context, id uuid.UUID) error {
	r.Lock()
	defer r.Unlock()

	if _, ok := r.upstream[id]; !ok {
		return ErrStreamNotFound
	}
	delete(r.upstream, id)
	return nil
}

// SaveDownstreamはメモリ内にストリームを保存します。
func (r *inmemStreamRepository) SaveDownstream(ctx context.Context, id uuid.UUID, info DownstreamState) (*DownstreamState, error) {
	r.Lock()
	defer r.Unlock()

	r.downstream[id] = &info
	return &info, nil
}

// FindDownstreamByIDはメモリ内に保存されたストリームから、引数idに合致するストリームを返します。
func (r *inmemStreamRepository) FindDownstreamByID(ctx context.Context, id uuid.UUID) (*DownstreamState, error) {
	r.RLock()
	defer r.RUnlock()
	res, ok := r.downstream[id]

	if !ok {
		return nil, ErrStreamNotFound
	}
	return res, nil
}

// RemoveDownstreamByIDはメモリ内に保存されたストリームから、引数idのストリームを削除します。
func (r *inmemStreamRepository) RemoveDownstreamByID(ctx context.Context, id uuid.UUID) error {
	r.Lock()
	defer r.Unlock()

	if _, ok := r.downstream[id]; !ok {
		return ErrStreamNotFound
	}
	delete(r.downstream, id)
	return nil
}
