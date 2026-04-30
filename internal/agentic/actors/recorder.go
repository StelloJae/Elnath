package actors

import (
	"context"
	"fmt"

	"github.com/stello/elnath/internal/agentic"
)

type Recorder struct {
	store *agentic.Store
}

func NewRecorder(store *agentic.Store) *Recorder {
	return &Recorder{store: store}
}

func (r *Recorder) CreateActor(ctx context.Context, actor agentic.AgentActor) (*agentic.AgentActor, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("agentic actors: recorder store is nil")
	}
	return r.store.CreateAgentActor(ctx, actor)
}

func (r *Recorder) UpdateActor(ctx context.Context, actor agentic.AgentActor) (*agentic.AgentActor, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("agentic actors: recorder store is nil")
	}
	return r.store.UpdateAgentActor(ctx, actor)
}

func (r *Recorder) CreateHandoff(ctx context.Context, handoff agentic.ActorHandoff) (*agentic.ActorHandoff, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("agentic actors: recorder store is nil")
	}
	return r.store.CreateActorHandoff(ctx, handoff)
}
