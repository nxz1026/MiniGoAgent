package session

import (
	"context"

	"MiniGoAgent/internal/adk/convert"
	adktypes "MiniGoAgent/internal/adk/types"
	appsession "MiniGoAgent/internal/session"
)

type ManagerAdapter struct {
	inner *appsession.Manager
	sid   string
}

func NewManagerAdapter(m *appsession.Manager, sessionID string) *ManagerAdapter {
	return &ManagerAdapter{inner: m, sid: sessionID}
}

func (a *ManagerAdapter) Get(ctx context.Context, sid string) ([]*adktypes.Message, error) {
	msgs := a.inner.SnapshotWith(sid)
	return convert.FromEinoSlice(msgs), nil
}

func (a *ManagerAdapter) Append(ctx context.Context, sid string, msgs ...*adktypes.Message) error {
	a.inner.Append(sid, convert.ToEinoSlice(msgs)...)
	return nil
}

func (a *ManagerAdapter) Snapshot(ctx context.Context, sid string) ([]*adktypes.Message, error) {
	msgs := a.inner.SnapshotWith(sid)
	return convert.FromEinoSlice(msgs), nil
}

func (a *ManagerAdapter) Inner() *appsession.Manager {
	return a.inner
}

func (a *ManagerAdapter) SessionID() string {
	return a.sid
}
