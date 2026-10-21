package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexProxyLatencyCacheStub struct {
	infos map[int64]*ProxyLatencyInfo
	err   error
}

func (s *codexProxyLatencyCacheStub) GetProxyLatencies(context.Context, []int64) (map[int64]*ProxyLatencyInfo, error) {
	return s.infos, s.err
}

func (s *codexProxyLatencyCacheStub) SetProxyLatency(context.Context, int64, *ProxyLatencyInfo) error {
	return nil
}

func TestCodexProxyTimezoneFromLatencyPrefersCachedTimezone(t *testing.T) {
	require.Equal(t, "Asia/Tokyo", codexProxyTimezoneFromLatency(&ProxyLatencyInfo{
		CountryCode: "US",
		Timezone:    "Asia/Tokyo",
	}))
}

func TestCodexProxyTimezoneFromLatencyFallsBackToCountry(t *testing.T) {
	require.Equal(t, "Asia/Tokyo", codexProxyTimezoneFromLatency(&ProxyLatencyInfo{
		CountryCode: "jp",
	}))
}

func TestCodexEnvironmentContextTimezoneUsesProxyCache(t *testing.T) {
	proxyID := int64(11)
	svc := &OpenAIGatewayService{proxyLatencyCache: &codexProxyLatencyCacheStub{
		infos: map[int64]*ProxyLatencyInfo{
			proxyID: &ProxyLatencyInfo{CountryCode: "JP", Timezone: "Asia/Tokyo"},
		},
	}}

	require.Equal(t, "Asia/Tokyo", svc.codexEnvironmentContextTimezone(context.Background(), &Account{ProxyID: &proxyID}))
}

func TestCodexEnvironmentContextTimezoneAccountOverrideWins(t *testing.T) {
	proxyID := int64(11)
	svc := &OpenAIGatewayService{proxyLatencyCache: &codexProxyLatencyCacheStub{
		infos: map[int64]*ProxyLatencyInfo{
			proxyID: &ProxyLatencyInfo{Timezone: "Asia/Tokyo"},
		},
	}}
	account := &Account{
		ProxyID: &proxyID,
		Extra: map[string]any{
			codexEnvironmentContextTimezoneExtraKey: "Asia/Seoul",
		},
	}

	require.Equal(t, "Asia/Seoul", svc.codexEnvironmentContextTimezone(context.Background(), account))
}

func TestCodexEnvironmentContextTimezoneFallbackWithoutProxy(t *testing.T) {
	svc := &OpenAIGatewayService{}

	require.Equal(t, codexEnvironmentContextDefaultTimezone, svc.codexEnvironmentContextTimezone(context.Background(), &Account{}))
}
