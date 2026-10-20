package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type gatewayRequestAuditStoreStub struct {
	state  GatewayRequestAuditState
	record *GatewayRequestAuditRecord
}

func (s *gatewayRequestAuditStoreStub) Begin(_ context.Context, id string, startedAt time.Time) (*GatewayRequestAuditState, error) {
	s.state = GatewayRequestAuditState{Active: true, SessionID: id, StartedAt: &startedAt}
	return &s.state, nil
}

func (s *gatewayRequestAuditStoreStub) End(_ context.Context, stoppedAt time.Time) (*GatewayRequestAuditState, error) {
	s.state.Active = false
	s.state.StoppedAt = &stoppedAt
	return &s.state, nil
}

func (s *gatewayRequestAuditStoreStub) Clear(context.Context) error {
	s.state = GatewayRequestAuditState{}
	s.record = nil
	return nil
}

func (s *gatewayRequestAuditStoreStub) State(context.Context) (*GatewayRequestAuditState, error) {
	return &s.state, nil
}

func (s *gatewayRequestAuditStoreStub) Append(_ context.Context, record *GatewayRequestAuditRecord) (bool, error) {
	if !s.state.Active {
		return false, nil
	}
	record.SessionID = s.state.SessionID
	s.record = record
	s.state.Count++
	return true, nil
}

func (s *gatewayRequestAuditStoreStub) List(context.Context) ([]*GatewayRequestAuditRecord, error) {
	if s.record == nil {
		return nil, nil
	}
	return []*GatewayRequestAuditRecord{s.record}, nil
}

func (s *gatewayRequestAuditStoreStub) Get(context.Context, string) (*GatewayRequestAuditRecord, error) {
	return s.record, nil
}

func TestGatewayRequestAuditCapturesHTTPHeadersAndBodies(t *testing.T) {
	store := &gatewayRequestAuditStoreStub{state: GatewayRequestAuditState{Active: true, SessionID: "session-1"}}
	svc := NewGatewayRequestAuditService(store)

	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Upstream", "yes")
	req = svc.AttachHTTP(req, GatewayRequestAuditDraft{
		Transport:         "http",
		AccountID:         42,
		DownstreamMethod:  http.MethodPost,
		DownstreamURL:     "/v1/responses",
		DownstreamHeaders: map[string][]string{"X-Downstream": {"yes"}},
		DownstreamBody:    []byte(`{"model":"client"}`),
	})

	CaptureGatewayRequestAudit(req)

	require.NotNil(t, store.record)
	require.Equal(t, "session-1", store.record.SessionID)
	require.Equal(t, `{"model":"client"}`, store.record.DownstreamBody)
	require.Equal(t, `{"model":"gpt-5"}`, store.record.UpstreamBody)
	require.Equal(t, []string{"yes"}, store.record.DownstreamHeaders["X-Downstream"])
	require.Equal(t, []string{"yes"}, store.record.UpstreamHeaders["X-Upstream"])
	require.Equal(t, int64(42), store.record.AccountID)
}
