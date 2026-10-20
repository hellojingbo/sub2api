package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	gatewayRequestAuditStateKey   = "sub2api:gateway-request-audit:state"
	gatewayRequestAuditRecordsKey = "sub2api:gateway-request-audit:records"
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
			pipe.Del(ctx, gatewayRequestAuditRecordsKey)
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
	return s.rdb.Del(ctx, gatewayRequestAuditStateKey, gatewayRequestAuditRecordsKey).Err()
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
	if record == nil {
		return false, errors.New("nil gateway request audit record")
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
	// The Lua transaction closes the stop/append race: either the record sees an
	// active state and increments the counters, or it is rejected after Stop.
	result, err := redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local state = cjson.decode(raw)
if not state['active'] then return 0 end
if state['session_id'] ~= ARGV[3] then return 0 end
redis.call('LPUSH', KEYS[2], ARGV[1])
state['count'] = (state['count'] or 0) + 1
state['bytes'] = (state['bytes'] or 0) + tonumber(ARGV[2])
redis.call('SET', KEYS[1], cjson.encode(state))
return 1
`).Run(ctx, s.rdb, []string{gatewayRequestAuditStateKey, gatewayRequestAuditRecordsKey}, string(payload), strconv.Itoa(len(payload)), record.SessionID).Result()
	if err != nil {
		return false, err
	}
	accepted, ok := result.(int64)
	return ok && accepted == 1, nil
}

func (s *gatewayRequestAuditStore) List(ctx context.Context) ([]*service.GatewayRequestAuditRecord, error) {
	if s == nil || s.rdb == nil {
		return nil, errors.New("gateway request audit store unavailable")
	}
	values, err := s.rdb.LRange(ctx, gatewayRequestAuditRecordsKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	records := make([]*service.GatewayRequestAuditRecord, 0, len(values))
	for _, raw := range values {
		var record service.GatewayRequestAuditRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			continue
		}
		records = append(records, &record)
	}
	return records, nil
}

func (s *gatewayRequestAuditStore) Get(ctx context.Context, id string) (*service.GatewayRequestAuditRecord, error) {
	if s == nil || s.rdb == nil {
		return nil, errors.New("gateway request audit store unavailable")
	}
	if id == "" {
		return nil, fmt.Errorf("gateway request audit id is required")
	}
	records, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record != nil && record.ID == id {
			return record, nil
		}
	}
	return nil, fmt.Errorf("gateway request audit record %q not found", id)
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
