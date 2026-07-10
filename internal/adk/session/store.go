package session

import (
	"context"

	adktypes "MiniGoAgent/internal/adk/types"
)

type Store interface {
	Get(ctx context.Context, sid string) ([]*adktypes.Message, error)
	Append(ctx context.Context, sid string, msgs ...*adktypes.Message) error
	Snapshot(ctx context.Context, sid string) ([]*adktypes.Message, error)
}
