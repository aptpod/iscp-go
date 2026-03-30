package iscp

import (
	"context"
	"sync"

	"github.com/aptpod/iscp-go/v2/errors"

	uuid "github.com/google/uuid"
)

// sentStorageは、送信済みのデータポイントを扱うストレージインターフェースです。
//
// ストレージに保存されたデータポイントはUpstreamChunkAckを受信した時点で削除します。
type sentStorage interface {
	// Storeは、データポイントを保存します。
	Store(ctx context.Context, streamID uuid.UUID, sequence uint32, dps DataPointGroups) error
	// Removeは、保存しているデータポイントを削除します。
	//
	// 削除したデータポイントを返却します。
	Remove(ctx context.Context, streamID uuid.UUID, sequence uint32) (DataPointGroups, error)

	// Listは、指定したストリームIDのデータポイントをすべて取得します。
	List(ctx context.Context, streamID uuid.UUID) (map[uint32]DataPointGroups, error)

	// Clearは、指定したストリームIDのデータポイントをすべて削除します。
	Clear(ctx context.Context, streamID uuid.UUID) error
}

// upstreamRepositoryは、アップストリーム情報のリポジトリインターフェースです。
type upstreamRepository interface {
	// SaveUpstreamは、アップストリーム情報を保存します。
	SaveUpstream(ctx context.Context, id uuid.UUID, info UpstreamState) (*UpstreamState, error)

	// RemoveUpstreamByIDは、指定したIDのアップストリーム情報を削除します。
	RemoveUpstreamByID(ctx context.Context, id uuid.UUID) error
}

// downstreamRepositoryは、ダウンストリーム情報のリポジトリインターフェースです。
type downstreamRepository interface {
	// SaveDownstreamは、ダウンストリーム情報を保存します。
	SaveDownstream(ctx context.Context, id uuid.UUID, info DownstreamState) (*DownstreamState, error)

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

// inmemSentStorageNoPayloadは、送信済みのデータポイントを扱うストレージインターフェースのインメモリ実装です。
//
// ただし、inmemSentStorageとは異なり、データペイロードは保存しません。
// Remove, List, Clear は埋め込みの inmemSentStorage に委譲されます。
type inmemSentStorageNoPayload struct {
	*inmemSentStorage
}

// newInmemSentStorageNoPayloadは、送信済みのデータポイントを扱うストレージインターフェースのインメモリ実装（ただしペイロードの保存はしない）です。
func newInmemSentStorageNoPayload() *inmemSentStorageNoPayload {
	return &inmemSentStorageNoPayload{
		inmemSentStorage: newInmemSentStorage(),
	}
}

// Storeは、送信済みのデータポイントをメモリ内に保存します。
// ペイロードを除去してから保存します。
func (s *inmemSentStorageNoPayload) Store(ctx context.Context, streamID uuid.UUID, sequenceNumber uint32, dps DataPointGroups) error {
	return s.inmemSentStorage.Store(ctx, streamID, sequenceNumber, dps.withoutPayload())
}

type inmemSentStorage struct {
	sync.RWMutex
	buf map[uuid.UUID]map[uint32]DataPointGroups
}

func newInmemSentStorage() *inmemSentStorage {
	return &inmemSentStorage{
		RWMutex: sync.RWMutex{},
		buf:     make(map[uuid.UUID]map[uint32]DataPointGroups),
	}
}

func (s *inmemSentStorage) Store(ctx context.Context, streamID uuid.UUID, sequenceNumber uint32, dps DataPointGroups) error {
	s.Lock()
	defer s.Unlock()
	if _, ok := s.buf[streamID]; !ok {
		s.buf[streamID] = map[uint32]DataPointGroups{}
	}
	s.buf[streamID][sequenceNumber] = dps
	return nil
}

func (s *inmemSentStorage) Remove(ctx context.Context, streamID uuid.UUID, sequenceNumber uint32) (DataPointGroups, error) {
	s.Lock()
	defer s.Unlock()
	if _, ok := s.buf[streamID]; !ok {
		return nil, errors.Errorf("not found stream %v", streamID.String())
	}
	res, ok := s.buf[streamID][sequenceNumber]
	if !ok {
		return nil, errors.Errorf("not found sequence number %v", sequenceNumber)
	}
	delete(s.buf[streamID], sequenceNumber)
	return res, nil
}

func (s *inmemSentStorage) List(ctx context.Context, streamID uuid.UUID) (map[uint32]DataPointGroups, error) {
	s.RLock()
	defer s.RUnlock()
	if _, ok := s.buf[streamID]; !ok {
		return nil, errors.Errorf("not found stream %v", streamID.String())
	}

	// Create a copy of the map
	result := make(map[uint32]DataPointGroups, len(s.buf[streamID]))
	for seq, dps := range s.buf[streamID] {
		result[seq] = dps
	}
	return result, nil
}

func (s *inmemSentStorage) Clear(ctx context.Context, streamID uuid.UUID) error {
	s.RLock()
	defer s.RUnlock()
	s.buf = make(map[uuid.UUID]map[uint32]DataPointGroups)
	return nil
}
