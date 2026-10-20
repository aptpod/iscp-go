package iscp

import (
	"context"
	"sync"

	uuid "github.com/google/uuid"
)

// upstreamRepository is the repository interface for upstream information.
type upstreamRepository interface {
	// SaveUpstream saves upstream information.
	SaveUpstream(ctx context.Context, id uuid.UUID, info UpstreamState) (*UpstreamState, error)

	// RemoveUpstreamByID removes the upstream information with the specified ID.
	RemoveUpstreamByID(ctx context.Context, id uuid.UUID) error
}

// downstreamRepository is the repository interface for downstream information.
type downstreamRepository interface {
	// SaveDownstream saves downstream information.
	SaveDownstream(ctx context.Context, id uuid.UUID, info DownstreamState) (*DownstreamState, error)

	// RemoveDownstreamByID removes the downstream information with the specified ID.
	RemoveDownstreamByID(ctx context.Context, id uuid.UUID) error
}

// inmemStreamRepository is the in-memory implementation of the stream information repository.
type inmemStreamRepository struct {
	sync.RWMutex
	upstream   map[uuid.UUID]*UpstreamState
	downstream map[uuid.UUID]*DownstreamState
}

// newInmemStreamRepository creates the in-memory implementation of the stream information repository.
func newInmemStreamRepository() *inmemStreamRepository {
	return &inmemStreamRepository{
		upstream:   make(map[uuid.UUID]*UpstreamState),
		downstream: make(map[uuid.UUID]*DownstreamState),
	}
}

// SaveUpstream saves the stream in memory.
func (r *inmemStreamRepository) SaveUpstream(ctx context.Context, id uuid.UUID, info UpstreamState) (*UpstreamState, error) {
	r.Lock()
	defer r.Unlock()

	r.upstream[id] = &info
	return &info, nil
}

// RemoveUpstreamByID removes the stream with the given id from the streams stored in memory.
func (r *inmemStreamRepository) RemoveUpstreamByID(ctx context.Context, id uuid.UUID) error {
	r.Lock()
	defer r.Unlock()

	if _, ok := r.upstream[id]; !ok {
		return ErrStreamNotFound
	}
	delete(r.upstream, id)
	return nil
}

// SaveDownstream saves the stream in memory.
func (r *inmemStreamRepository) SaveDownstream(ctx context.Context, id uuid.UUID, info DownstreamState) (*DownstreamState, error) {
	r.Lock()
	defer r.Unlock()

	r.downstream[id] = &info
	return &info, nil
}

// RemoveDownstreamByID removes the stream with the given id from the streams stored in memory.
func (r *inmemStreamRepository) RemoveDownstreamByID(ctx context.Context, id uuid.UUID) error {
	r.Lock()
	defer r.Unlock()

	if _, ok := r.downstream[id]; !ok {
		return ErrStreamNotFound
	}
	delete(r.downstream, id)
	return nil
}
