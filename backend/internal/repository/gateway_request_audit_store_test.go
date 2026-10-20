package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestGatewayRequestAuditStoreManualSessionLifecycle(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewGatewayRequestAuditStore(client)
	ctx := context.Background()

	startedAt := time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
	state, err := store.Begin(ctx, "session-1", startedAt)
	require.NoError(t, err)
	require.True(t, state.Active)

	record := &service.GatewayRequestAuditRecord{ID: "record-1", DownstreamBody: "downstream", UpstreamBody: "upstream"}
	accepted, err := store.Append(ctx, record)
	require.NoError(t, err)
	require.True(t, accepted)

	state, err = store.State(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, state.Count)

	records, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "session-1", records[0].SessionID)

	stopped, err := store.End(ctx, startedAt.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, stopped.Active)
	accepted, err = store.Append(ctx, &service.GatewayRequestAuditRecord{ID: "record-2"})
	require.NoError(t, err)
	require.False(t, accepted)

	require.NoError(t, store.Clear(ctx))
	cleared, err := store.State(ctx)
	require.NoError(t, err)
	require.False(t, cleared.Active)
	require.Empty(t, cleared.SessionID)
}

func TestGatewayRequestAuditStoreRejectsConcurrentStart(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewGatewayRequestAuditStore(client)
	ctx := context.Background()

	_, err := store.Begin(ctx, "session-1", time.Now().UTC())
	require.NoError(t, err)
	_, err = store.Begin(ctx, "session-2", time.Now().UTC())
	require.ErrorIs(t, err, service.ErrGatewayRequestAuditActive)
}
