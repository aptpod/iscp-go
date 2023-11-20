package iscp

import (
	"context"
	"sync"

	"github.com/aptpod/iscp-go/errors"

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

// inmemSentStorageNoPayloadは、送信済みのデータポイントを扱うストレージインターフェースのインメモリ実装です。
//
// ただし、InmemSentStorageは異なり、データペイロードは保存しません。
type inmemSentStorageNoPayload struct {
	s *inmemSentStorage
}

// newInmemSentStorageNoPayloadは、送信済みのデータポイントを扱うストレージインターフェースのインメモリ実装（ただしペイロードの保存はしない）です。
func newInmemSentStorageNoPayload() *inmemSentStorageNoPayload {
	return &inmemSentStorageNoPayload{
		s: newInmemSentStorage(),
	}
}

// Storeは、送信済みのデータポイントをメモリ内に保存します。
func (s *inmemSentStorageNoPayload) Store(ctx context.Context, streamID uuid.UUID, sequenceNumber uint32, dps DataPointGroups) error {
	return s.s.Store(ctx, streamID, sequenceNumber, dps.withoutPayload())
}

// Removeは、メモリ内に保存された送信済みのデータポイントから、指定されたstreamIDとシーケンス番号に紐づいているデータポイントを削除します。
func (s *inmemSentStorageNoPayload) Remove(ctx context.Context, streamID uuid.UUID, sequenceNumber uint32) (DataPointGroups, error) {
	return s.s.Remove(ctx, streamID, sequenceNumber)
}

func (s *inmemSentStorageNoPayload) List(ctx context.Context, streamID uuid.UUID) (map[uint32]DataPointGroups, error) {
	return s.s.List(ctx, streamID)
}

func (s *inmemSentStorageNoPayload) Clear(ctx context.Context, streamID uuid.UUID) error {
	return s.s.Clear(ctx, streamID)
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
	return s.buf[streamID], nil
}

func (s *inmemSentStorage) Clear(ctx context.Context, streamID uuid.UUID) error {
	s.RLock()
	defer s.RUnlock()
	s.buf = make(map[uuid.UUID]map[uint32]DataPointGroups)
	return nil
}
