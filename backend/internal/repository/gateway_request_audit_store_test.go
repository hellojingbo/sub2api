package repository

import (
	"context"
	"encoding/json"
	"strings"
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

	records, total, err := store.List(ctx, 0, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, []service.GatewayRequestAuditSummary{record.Summary()}, records)
	detail, err := store.Get(ctx, "record-1")
	require.NoError(t, err)
	require.Equal(t, record, detail)

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
	require.Zero(t, client.Exists(ctx, auditCaptureKeys...).Val())
	_, err = store.Get(ctx, "record-1")
	require.ErrorIs(t, err, service.ErrGatewayRequestAuditRecordNotFound)
}

func TestGatewayRequestAuditStorePagesNewAndLegacyRecords(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewGatewayRequestAuditStore(client)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := store.Begin(ctx, "session-1", now)
	require.NoError(t, err)

	// Seed the layout used by the previous release, which has no index.
	legacy := service.GatewayRequestAuditRecord{
		ID: "legacy", SessionID: "session-1", CapturedAt: now, Transport: "http",
		UpstreamHeaders: map[string][]string{"Content-Type": {"application/json"}},
		UpstreamBody:    strings.Repeat("a", 1<<20),
	}
	legacyJSON, err := json.Marshal(legacy)
	require.NoError(t, err)
	legacyNewer := service.GatewayRequestAuditRecord{
		ID: "legacy-newer", SessionID: "session-1", CapturedAt: now, Transport: "ws",
	}
	legacyNewerJSON, err := json.Marshal(legacyNewer)
	require.NoError(t, err)
	require.NoError(t, client.LPush(ctx, gatewayRequestAuditRecordsKey, legacyJSON, legacyNewerJSON).Err())

	newer := service.GatewayRequestAuditRecord{ID: "newer", CapturedAt: now.Add(time.Millisecond)}
	newest := service.GatewayRequestAuditRecord{ID: "newest", CapturedAt: now.Add(2 * time.Millisecond)}
	for _, record := range []*service.GatewayRequestAuditRecord{&newer, &newest, &newest} {
		accepted, err := store.Append(ctx, record)
		require.NoError(t, err)
		require.True(t, accepted)
	}

	firstPage, total, err := store.List(ctx, 0, 2)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	require.Equal(t, []service.GatewayRequestAuditSummary{newest.Summary(), newer.Summary()}, firstPage)

	legacyPage, total, err := store.List(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	require.Equal(t, []service.GatewayRequestAuditSummary{legacyNewer.Summary()}, legacyPage)
	require.True(t, client.HExists(ctx, gatewayRequestAuditIndexKey, "id:legacy-newer").Val())
	require.False(t, client.HExists(ctx, gatewayRequestAuditIndexKey, "id:legacy").Val())

	// A direct legacy detail link still resolves before loading its page.
	detail, err := store.Get(ctx, "legacy")
	require.NoError(t, err)
	require.Equal(t, &legacy, detail)
	lastPage, total, err := store.List(ctx, 3, 2)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	require.Equal(t, []service.GatewayRequestAuditSummary{legacy.Summary()}, lastPage)
	summaryJSON, err := json.Marshal(lastPage)
	require.NoError(t, err)
	require.NotContains(t, string(summaryJSON), "upstream_headers")
	require.NotContains(t, string(summaryJSON), "upstream_body")
	require.Less(t, len(summaryJSON), 300)

	beyond, total, err := store.List(ctx, 30, 2)
	require.NoError(t, err)
	require.Equal(t, int64(4), total)
	require.Empty(t, beyond)

	_, err = store.End(ctx, now)
	require.NoError(t, err)
	_, err = store.Begin(ctx, "session-2", now)
	require.NoError(t, err)
	_, err = store.Get(ctx, "legacy")
	require.ErrorIs(t, err, service.ErrGatewayRequestAuditRecordNotFound)
	require.Zero(t, client.Exists(ctx, gatewayRequestAuditIndexKey, gatewayRequestAuditRecordsKey).Val())
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
