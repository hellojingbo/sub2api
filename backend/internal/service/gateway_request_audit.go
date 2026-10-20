package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ErrGatewayRequestAuditActive indicates that a capture session is already running.
var ErrGatewayRequestAuditActive = errors.New("gateway request audit capture is already active")

// GatewayRequestAuditState describes the manually controlled capture session.
type GatewayRequestAuditState struct {
	Active    bool       `json:"active"`
	SessionID string     `json:"session_id"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	Count     int        `json:"count"`
	Bytes     int64      `json:"bytes"`
}

// GatewayRequestAuditRecord stores one downstream/upstream request pair.
// Header values are intentionally retained in full because this is an explicit,
// admin-only capture session rather than the ordinary redacted operation log.
type GatewayRequestAuditRecord struct {
	ID                  string              `json:"id"`
	CapturedAt          time.Time           `json:"captured_at"`
	SessionID           string              `json:"session_id"`
	Transport           string              `json:"transport"`
	AccountID           int64               `json:"account_id,omitempty"`
	AccountName         string              `json:"account_name,omitempty"`
	RequestID           string              `json:"request_id,omitempty"`
	DownstreamMethod    string              `json:"downstream_method"`
	DownstreamURL       string              `json:"downstream_url"`
	DownstreamHeaders   map[string][]string `json:"downstream_headers"`
	DownstreamBody      string              `json:"downstream_body,omitempty"`
	DownstreamBodyCodec string              `json:"downstream_body_codec,omitempty"`
	UpstreamMethod      string              `json:"upstream_method"`
	UpstreamURL         string              `json:"upstream_url"`
	UpstreamHeaders     map[string][]string `json:"upstream_headers"`
	UpstreamBody        string              `json:"upstream_body,omitempty"`
	UpstreamBodyCodec   string              `json:"upstream_body_codec,omitempty"`
	UpstreamWireBody    string              `json:"upstream_wire_body,omitempty"`
	UpstreamWireCodec   string              `json:"upstream_wire_body_codec,omitempty"`
}

// GatewayRequestAuditDraft carries the downstream half until an upstream
// request reaches its final transport boundary.
type GatewayRequestAuditDraft struct {
	Transport           string
	AccountID           int64
	AccountName         string
	RequestID           string
	DownstreamMethod    string
	DownstreamURL       string
	DownstreamHeaders   map[string][]string
	DownstreamBody      []byte
	UpstreamLogicalBody []byte
}

// GatewayRequestAuditStore is the persistence port for the manual capture
// session. The Redis implementation keeps state shared across instances.
type GatewayRequestAuditStore interface {
	Begin(ctx context.Context, sessionID string, startedAt time.Time) (*GatewayRequestAuditState, error)
	End(ctx context.Context, stoppedAt time.Time) (*GatewayRequestAuditState, error)
	Clear(ctx context.Context) error
	State(ctx context.Context) (*GatewayRequestAuditState, error)
	Append(ctx context.Context, record *GatewayRequestAuditRecord) (bool, error)
	List(ctx context.Context) ([]*GatewayRequestAuditRecord, error)
	Get(ctx context.Context, id string) (*GatewayRequestAuditRecord, error)
}

// GatewayRequestAuditService coordinates the UI controls and gateway capture
// callbacks. Capture errors are deliberately best-effort and never fail the
// user's upstream request.
type GatewayRequestAuditService struct {
	store GatewayRequestAuditStore
}

// NewGatewayRequestAuditService creates the manual request capture service.
func NewGatewayRequestAuditService(store GatewayRequestAuditStore) *GatewayRequestAuditService {
	return &GatewayRequestAuditService{store: store}
}

// Start begins a fresh capture session and discards the previous result set.
func (s *GatewayRequestAuditService) Start(ctx context.Context) (*GatewayRequestAuditState, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("gateway request audit service unavailable")
	}
	return s.store.Begin(ctx, uuid.NewString(), time.Now().UTC())
}

// Stop ends the current capture session while preserving its records.
func (s *GatewayRequestAuditService) Stop(ctx context.Context) (*GatewayRequestAuditState, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("gateway request audit service unavailable")
	}
	return s.store.End(ctx, time.Now().UTC())
}

// Clear removes the current capture result and returns the idle state.
func (s *GatewayRequestAuditService) Clear(ctx context.Context) error {
	if s == nil || s.store == nil {
		return errors.New("gateway request audit service unavailable")
	}
	return s.store.Clear(ctx)
}

// State returns the current manual capture state.
func (s *GatewayRequestAuditService) State(ctx context.Context) (*GatewayRequestAuditState, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("gateway request audit service unavailable")
	}
	return s.store.State(ctx)
}

// List returns the current session's records, newest first.
func (s *GatewayRequestAuditService) List(ctx context.Context) ([]*GatewayRequestAuditRecord, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("gateway request audit service unavailable")
	}
	return s.store.List(ctx)
}

// Get returns one captured request pair.
func (s *GatewayRequestAuditService) Get(ctx context.Context, id string) (*GatewayRequestAuditRecord, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("gateway request audit service unavailable")
	}
	return s.store.Get(ctx, strings.TrimSpace(id))
}

// AttachHTTP adds a callback to the request context. The callback is invoked
// by the transport after any request compression, so the saved upstream body
// matches the bytes that are about to leave the process.
func (s *GatewayRequestAuditService) AttachHTTP(req *http.Request, draft GatewayRequestAuditDraft) *http.Request {
	if s == nil || s.store == nil || req == nil {
		return req
	}
	if len(draft.UpstreamLogicalBody) == 0 {
		draft.UpstreamLogicalBody = append([]byte(nil), readGatewayRequestBody(req)...)
	}
	callback := func(upstream *http.Request) {
		s.recordHTTP(draft, upstream)
	}
	return req.WithContext(withGatewayRequestAuditCapture(req.Context(), callback))
}

// CaptureGatewayRequestAudit invokes the callback attached to an HTTP request.
// Repository transports call this after their final body/header normalization.
func CaptureGatewayRequestAudit(req *http.Request) {
	if req == nil {
		return
	}
	if callback, ok := req.Context().Value(gatewayRequestAuditCaptureKey{}).(func(*http.Request)); ok && callback != nil {
		callback(req)
	}
}

// RecordWebSocket records one WebSocket turn after it has been written
// upstream. The handshake headers represent the upstream header set; the turn
// payload represents the upstream body.
func (s *GatewayRequestAuditService) RecordWebSocket(
	ctx context.Context,
	draft GatewayRequestAuditDraft,
	upstreamURL string,
	upstreamHeaders map[string][]string,
	upstreamBody []byte,
) {
	if s == nil || s.store == nil {
		return
	}
	upstreamText, upstreamCodec := encodeGatewayRequestAuditBody(upstreamBody)
	downstreamText, downstreamCodec := encodeGatewayRequestAuditBody(draft.DownstreamBody)
	record := &GatewayRequestAuditRecord{
		ID:                  uuid.NewString(),
		CapturedAt:          time.Now().UTC(),
		Transport:           defaultAuditTransport(draft.Transport, "websocket"),
		AccountID:           draft.AccountID,
		AccountName:         draft.AccountName,
		RequestID:           draft.RequestID,
		DownstreamMethod:    defaultAuditMethod(draft.DownstreamMethod, "WS"),
		DownstreamURL:       draft.DownstreamURL,
		DownstreamHeaders:   cloneAuditHeaders(draft.DownstreamHeaders),
		DownstreamBody:      downstreamText,
		DownstreamBodyCodec: downstreamCodec,
		UpstreamMethod:      "WS",
		UpstreamURL:         upstreamURL,
		UpstreamHeaders:     cloneAuditHeaders(upstreamHeaders),
		UpstreamBody:        upstreamText,
		UpstreamBodyCodec:   upstreamCodec,
	}
	s.appendBestEffort(ctx, record)
}

func (s *GatewayRequestAuditService) recordHTTP(draft GatewayRequestAuditDraft, upstream *http.Request) {
	if upstream == nil {
		return
	}
	upstreamBody := readGatewayRequestBody(upstream)
	downstreamText, downstreamCodec := encodeGatewayRequestAuditBody(draft.DownstreamBody)
	upstreamText, upstreamCodec := encodeGatewayRequestAuditBody(draft.UpstreamLogicalBody)
	if len(draft.UpstreamLogicalBody) == 0 {
		upstreamText, upstreamCodec = encodeGatewayRequestAuditBody(upstreamBody)
	}
	wireText, wireCodec := encodeGatewayRequestAuditBody(upstreamBody)
	if wireCodec == "utf8" && wireText == upstreamText {
		wireText, wireCodec = "", ""
	}
	upstreamURL := ""
	if upstream.URL != nil {
		upstreamURL = upstream.URL.String()
	}
	record := &GatewayRequestAuditRecord{
		ID:                  uuid.NewString(),
		CapturedAt:          time.Now().UTC(),
		Transport:           defaultAuditTransport(draft.Transport, "http"),
		AccountID:           draft.AccountID,
		AccountName:         draft.AccountName,
		RequestID:           draft.RequestID,
		DownstreamMethod:    defaultAuditMethod(draft.DownstreamMethod, "POST"),
		DownstreamURL:       draft.DownstreamURL,
		DownstreamHeaders:   cloneAuditHeaders(draft.DownstreamHeaders),
		DownstreamBody:      downstreamText,
		DownstreamBodyCodec: downstreamCodec,
		UpstreamMethod:      upstream.Method,
		UpstreamURL:         upstreamURL,
		UpstreamHeaders:     cloneAuditHeaders(upstream.Header),
		UpstreamBody:        upstreamText,
		UpstreamBodyCodec:   upstreamCodec,
		UpstreamWireBody:    wireText,
		UpstreamWireCodec:   wireCodec,
	}
	s.appendBestEffort(context.Background(), record)
}

func (s *GatewayRequestAuditService) appendBestEffort(ctx context.Context, record *GatewayRequestAuditRecord) {
	if s == nil || s.store == nil || record == nil {
		return
	}
	if _, err := s.store.Append(ctx, record); err != nil {
		// Capture is diagnostic and must never change gateway behavior.
		return
	}
}

type gatewayRequestAuditCaptureKey struct{}

func withGatewayRequestAuditCapture(ctx context.Context, callback func(*http.Request)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gatewayRequestAuditCaptureKey{}, callback)
}

func readGatewayRequestBody(req *http.Request) []byte {
	if req == nil || req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil && body != nil {
			defer func() { _ = body.Close() }()
			data, _ := io.ReadAll(body)
			return data
		}
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}
	restored := append([]byte(nil), data...)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(restored))
	return data
}

func encodeGatewayRequestAuditBody(raw []byte) (string, string) {
	if len(raw) == 0 {
		return "", "utf8"
	}
	if utf8.Valid(raw) {
		return string(raw), "utf8"
	}
	return base64.StdEncoding.EncodeToString(raw), "base64"
}

func cloneAuditHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return map[string][]string{}
	}
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func defaultAuditTransport(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultAuditMethod(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
