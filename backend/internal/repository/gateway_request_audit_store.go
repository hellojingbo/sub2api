package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	gatewayRequestAuditStateKey   = "sub2api:gateway-request-audit:state"
	gatewayRequestAuditRecordsKey = "sub2api:gateway-request-audit:records"
	gatewayRequestAuditIndexKey   = "sub2api:gateway-request-audit:index"
)

// The list retains the original full records. A position counted from its tail
// stays stable as new records are prepended. The index stores summaries by
// position and maps record IDs to those positions for single-record reads.
const gatewayRequestAuditIndexLua = `
local function index_record(key, session_id, position, id, summary)
    if redis.call('HGET', key, 'session_id') ~= session_id then
        redis.call('UNLINK', key)
    end
    redis.call('HSET', key, 'session_id', session_id,
        'summary:' .. position, summary, 'id:' .. id, position)
end
`

var (
	gatewayRequestAuditAppendScript = redis.NewScript(gatewayRequestAuditIndexLua + `
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local state = cjson.decode(raw)
if not state.active or state.session_id ~= ARGV[2] then return 0 end
if redis.call('HGET', KEYS[3], 'session_id') == state.session_id
    and redis.call('HEXISTS', KEYS[3], 'id:' .. ARGV[3]) == 1 then return 1 end
local position = redis.call('LPUSH', KEYS[2], ARGV[1])
index_record(KEYS[3], state.session_id, position, ARGV[3], ARGV[4])
state.count = (state.count or 0) + 1
state.bytes = (state.bytes or 0) + #ARGV[1]
redis.call('SET', KEYS[1], cjson.encode(state))
return 1
`)

	gatewayRequestAuditListScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return {0, ''} end
local session_id = cjson.decode(raw).session_id
local total = redis.call('LLEN', KEYS[2])
local page = {total, session_id}
local indexed = redis.call('HGET', KEYS[3], 'session_id') == session_id
local first = total - tonumber(ARGV[1])
local last = math.max(1, first - tonumber(ARGV[2]) + 1)
for position = first, last, -1 do
    page[#page + 1] = indexed and redis.call('HGET', KEYS[3], 'summary:' .. position) or false
end
return page
`)

	gatewayRequestAuditGetScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return {} end
local session_id = cjson.decode(raw).session_id
local total = redis.call('LLEN', KEYS[2])
if redis.call('HGET', KEYS[3], 'session_id') == session_id then
    local position = redis.call('HGET', KEYS[3], 'id:' .. ARGV[1])
    if position then return {redis.call('LINDEX', KEYS[2], -tonumber(position))} end
    if redis.call('HLEN', KEYS[3]) == total * 2 + 1 then return {} end
end
return {total, session_id}
`)

	gatewayRequestAuditLegacyScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw or cjson.decode(raw).session_id ~= ARGV[1] then return {} end
if redis.call('HGET', KEYS[3], 'session_id') == ARGV[1] then
    local summary = redis.call('HGET', KEYS[3], 'summary:' .. ARGV[2])
    if summary then return {summary, false} end
end
return {false, redis.call('LINDEX', KEYS[2], -tonumber(ARGV[2]))}
`)

	gatewayRequestAuditCacheSummaryScript = redis.NewScript(gatewayRequestAuditIndexLua + `
local raw = redis.call('GET', KEYS[1])
if not raw or cjson.decode(raw).session_id ~= ARGV[1] then return 0 end
index_record(KEYS[2], ARGV[1], ARGV[2], ARGV[3], ARGV[4])
return 1
`)
	auditCaptureKeys = []string{gatewayRequestAuditStateKey, gatewayRequestAuditRecordsKey, gatewayRequestAuditIndexKey}
)

// gatewayRequestAuditStore keeps one explicit capture session in Redis. The
// records list is intentionally unbounded by count: the operator controls the
// session lifetime with Start and Stop, and Clear removes it explicitly.
type gatewayRequestAuditStore struct {
	rdb *redis.Client
}

// NewGatewayRequestAuditStore creates the shared Redis capture store.
func NewGatewayRequestAuditStore(rdb *redis.Client) service.GatewayRequestAuditStore {
	return &gatewayRequestAuditStore{rdb: rdb}
}

func (s *gatewayRequestAuditStore) Begin(ctx context.Context, sessionID string, startedAt time.Time) (*service.GatewayRequestAuditState, error) {
	if s == nil || s.rdb == nil {
		return nil, errors.New("gateway request audit store unavailable")
	}
	state := &service.GatewayRequestAuditState{
		Active:    true,
		SessionID: sessionID,
		StartedAt: timePtr(startedAt.UTC()),
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var result service.GatewayRequestAuditState
	err = s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		current, getErr := readGatewayRequestAuditStateFromRedis(ctx, tx)
		if getErr != nil {
			return getErr
		}
		if current.Active {
			return service.ErrGatewayRequestAuditActive
		}
		_, txErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Unlink(ctx, gatewayRequestAuditRecordsKey, gatewayRequestAuditIndexKey)
			pipe.Set(ctx, gatewayRequestAuditStateKey, encoded, 0)
			return nil
		})
		if txErr == nil {
			result = *state
		}
		return txErr
	}, gatewayRequestAuditStateKey)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *gatewayRequestAuditStore) End(ctx context.Context, stoppedAt time.Time) (*service.GatewayRequestAuditState, error) {
	if s == nil || s.rdb == nil {
		return nil, errors.New("gateway request audit store unavailable")
	}
	var result service.GatewayRequestAuditState
	err := s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		state, getErr := readGatewayRequestAuditStateFromRedis(ctx, tx)
		if getErr != nil {
			return getErr
		}
		if state.Active {
			state.Active = false
			state.StoppedAt = timePtr(stoppedAt.UTC())
			encoded, marshalErr := json.Marshal(state)
			if marshalErr != nil {
				return marshalErr
			}
			if _, txErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, gatewayRequestAuditStateKey, encoded, 0)
				return nil
			}); txErr != nil {
				return txErr
			}
		}
		result = *state
		return nil
	}, gatewayRequestAuditStateKey)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *gatewayRequestAuditStore) Clear(ctx context.Context) error {
	if s == nil || s.rdb == nil {
		return errors.New("gateway request audit store unavailable")
	}
	return s.rdb.Unlink(ctx, auditCaptureKeys...).Err()
}

func (s *gatewayRequestAuditStore) State(ctx context.Context) (*service.GatewayRequestAuditState, error) {
	if s == nil || s.rdb == nil {
		return nil, errors.New("gateway request audit store unavailable")
	}
	return readGatewayRequestAuditStateFromRedis(ctx, s.rdb)
}

func (s *gatewayRequestAuditStore) Append(ctx context.Context, record *service.GatewayRequestAuditRecord) (bool, error) {
	if s == nil || s.rdb == nil {
		return false, errors.New("gateway request audit store unavailable")
	}
	if record == nil || record.ID == "" {
		return false, errors.New("gateway request audit record ID is required")
	}
	state, err := s.State(ctx)
	if err != nil {
		return false, err
	}
	if !state.Active {
		return false, nil
	}
	record.SessionID = state.SessionID
	payload, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	summary, err := json.Marshal(record.Summary())
	if err != nil {
		return false, err
	}
	accepted, err := gatewayRequestAuditAppendScript.Run(ctx, s.rdb, auditCaptureKeys,
		payload, record.SessionID, record.ID, summary).Int()
	return accepted == 1, err
}

func (s *gatewayRequestAuditStore) List(ctx context.Context, offset int64, limit int) ([]service.GatewayRequestAuditSummary, int64, error) {
	if s == nil || s.rdb == nil {
		return nil, 0, errors.New("gateway request audit store unavailable")
	}
	page, err := gatewayRequestAuditListScript.Run(ctx, s.rdb, auditCaptureKeys, offset, limit).Slice()
	if err != nil {
		return nil, 0, err
	}
	total, sessionID := page[0].(int64), page[1].(string)
	summaries := make([]service.GatewayRequestAuditSummary, 0, len(page)-2)
	for i, entry := range page[2:] {
		if summaryJSON, ok := entry.(string); ok {
			var summary service.GatewayRequestAuditSummary
			if err := json.Unmarshal([]byte(summaryJSON), &summary); err != nil {
				return nil, 0, err
			}
			summaries = append(summaries, summary)
			continue
		}
		position := total - offset - int64(i)
		summary, _, err := s.loadLegacySummary(ctx, sessionID, position)
		if err == redis.Nil {
			return summaries[:0], 0, nil
		}
		if err != nil {
			return nil, 0, err
		}
		if summary != nil {
			summaries = append(summaries, *summary)
		}
	}
	return summaries, total, nil
}

func (s *gatewayRequestAuditStore) Get(ctx context.Context, id string) (*service.GatewayRequestAuditRecord, error) {
	if s == nil || s.rdb == nil {
		return nil, errors.New("gateway request audit store unavailable")
	}
	if id == "" {
		return nil, service.ErrGatewayRequestAuditRecordNotFound
	}
	result, err := gatewayRequestAuditGetScript.Run(ctx, s.rdb, auditCaptureKeys, id).Slice()
	if err != nil {
		return nil, err
	}
	switch len(result) {
	case 0:
		return nil, service.ErrGatewayRequestAuditRecordNotFound
	case 1:
		payload, _ := result[0].(string)
		return decodeGatewayRequestAuditRecord(payload, id, "")
	}

	// Direct links to records captured by older versions may precede the first
	// list read. Project those records one at a time until the ID is indexed.
	total, sessionID := result[0].(int64), result[1].(string)
	for position := total; position > 0; position-- {
		summary, payload, err := s.loadLegacySummary(ctx, sessionID, position)
		if err == redis.Nil {
			break
		}
		if err != nil {
			return nil, err
		}
		if summary == nil || summary.ID != id {
			continue
		}
		if payload == "" {
			payload, err = s.rdb.LIndex(ctx, gatewayRequestAuditRecordsKey, -position).Result()
			if err == redis.Nil {
				break
			}
			if err != nil {
				return nil, err
			}
		}
		return decodeGatewayRequestAuditRecord(payload, id, sessionID)
	}
	return nil, service.ErrGatewayRequestAuditRecordNotFound
}

// Older versions only stored the full list. Read an individual legacy record
// and cache its projection while the same session still owns the list.
func (s *gatewayRequestAuditStore) loadLegacySummary(ctx context.Context, sessionID string, position int64) (*service.GatewayRequestAuditSummary, string, error) {
	result, err := gatewayRequestAuditLegacyScript.Run(ctx, s.rdb, auditCaptureKeys, sessionID, position).Slice()
	if err != nil {
		return nil, "", err
	}
	if len(result) == 0 {
		return nil, "", redis.Nil
	}
	if cached, ok := result[0].(string); ok {
		var summary service.GatewayRequestAuditSummary
		err := json.Unmarshal([]byte(cached), &summary)
		return &summary, "", err
	}
	payload, _ := result[1].(string)
	var record struct {
		service.GatewayRequestAuditSummary
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(payload), &record) != nil || record.ID == "" || record.SessionID != sessionID {
		return nil, "", nil
	}
	summary, err := json.Marshal(record.GatewayRequestAuditSummary)
	if err != nil {
		return nil, "", err
	}
	indexed, err := gatewayRequestAuditCacheSummaryScript.Run(ctx, s.rdb,
		[]string{gatewayRequestAuditStateKey, gatewayRequestAuditIndexKey},
		sessionID, position, record.ID, summary).Int()
	if err == nil && indexed == 0 {
		err = redis.Nil
	}
	return &record.GatewayRequestAuditSummary, payload, err
}

func decodeGatewayRequestAuditRecord(payload, id, sessionID string) (*service.GatewayRequestAuditRecord, error) {
	if payload == "" {
		return nil, service.ErrGatewayRequestAuditRecordNotFound
	}
	var record service.GatewayRequestAuditRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return nil, err
	}
	if record.ID != id || (sessionID != "" && record.SessionID != sessionID) {
		return nil, service.ErrGatewayRequestAuditRecordNotFound
	}
	return &record, nil
}

func readGatewayRequestAuditStateFromRedis(ctx context.Context, client interface {
	Get(context.Context, string) *redis.StringCmd
}) (*service.GatewayRequestAuditState, error) {
	raw, err := client.Get(ctx, gatewayRequestAuditStateKey).Result()
	if err == redis.Nil {
		return &service.GatewayRequestAuditState{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state service.GatewayRequestAuditState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func timePtr(value time.Time) *time.Time {
	return &value
}
