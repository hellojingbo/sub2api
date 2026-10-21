package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexAccountIdentityRepoStub struct {
	AccountRepository
	account *Account
}

func (s *codexAccountIdentityRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	return s.account, nil
}

func TestCodexRequestBodyIdentityNamespaceIsStablePerOAuthAccount(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-codex","prompt_cache_key":"client-session","client_metadata":{"x-codex-installation-id":"client-installation","session_id":"client-session","thread_id":"client-thread","x-codex-window-id":"client-window","x-codex-turn-metadata":"{\"installation_id\":\"client-installation\",\"session_id\":\"client-session\",\"thread_id\":\"client-thread\",\"turn_id\":\"client-turn\",\"window_id\":\"client-window\"}"}}`)
	account11 := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-account-11"}}
	account19 := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-account-19"}}

	first, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account11, 77)
	require.NoError(t, err)
	require.True(t, changed)
	firstAgain, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account11, 77)
	require.NoError(t, err)
	require.True(t, changed)
	second, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account19, 77)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, string(first), string(firstAgain))

	paths := []string{
		"prompt_cache_key",
		"client_metadata.x-codex-installation-id",
		"client_metadata.session_id",
		"client_metadata.thread_id",
		"client_metadata.x-codex-window-id",
	}
	for _, path := range paths {
		require.NotEqual(t, gjson.GetBytes(body, path).String(), gjson.GetBytes(first, path).String(), path)
		require.NotEqual(t, gjson.GetBytes(first, path).String(), gjson.GetBytes(second, path).String(), path)
	}
	require.Equal(t, gjson.GetBytes(first, "prompt_cache_key").String(), gjson.GetBytes(first, "client_metadata.session_id").String())

	var embeddedFirst map[string]any
	var embeddedSecond map[string]any
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(first, "client_metadata.x-codex-turn-metadata").String()), &embeddedFirst))
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(second, "client_metadata.x-codex-turn-metadata").String()), &embeddedSecond))
	for _, field := range []string{"installation_id", "session_id", "thread_id", "turn_id", "window_id"} {
		require.NotEqual(t, embeddedFirst[field], embeddedSecond[field], field)
	}
}

func TestCodexAccountIdentityNamespaceUsesStableCredentialSource(t *testing.T) {
	firstRow := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "shared-upstream-account"}}
	secondRow := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "shared-upstream-account"}}
	require.Equal(t, codexAccountIdentityNamespace(firstRow), codexAccountIdentityNamespace(secondRow))

	firstUser := &Account{ID: 20, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "team-account", "chatgpt_user_id": "user-1"}}
	sameUser := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "team-account", "chatgpt_user_id": "user-1"}}
	secondUser := &Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "team-account", "chatgpt_user_id": "user-2"}}
	require.Equal(t, codexAccountIdentityNamespace(firstUser), codexAccountIdentityNamespace(sameUser))
	require.NotEqual(t, codexAccountIdentityNamespace(firstUser), codexAccountIdentityNamespace(secondUser))

	seed := "11111111-1111-4111-8111-111111111111"
	seeded := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{codexFingerprintSeedExtraKey: seed}}
	require.Equal(t, "seed:"+seed, codexAccountIdentityNamespace(seeded))

	// Local row IDs repeat across independent deployments, so they are not a
	// safe fallback for upstream identity.
	require.Empty(t, codexAccountIdentityNamespace(&Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	oauthTokenA := &Account{ID: 40, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "oauth-token-a"}}
	oauthTokenADuplicate := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "oauth-token-a"}}
	oauthTokenB := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "oauth-token-b"}}
	require.Equal(t, codexAccountIdentityNamespace(oauthTokenA), codexAccountIdentityNamespace(oauthTokenADuplicate))
	require.NotEqual(t, codexAccountIdentityNamespace(oauthTokenA), codexAccountIdentityNamespace(oauthTokenB))
	require.NotContains(t, codexAccountIdentityNamespace(oauthTokenA), "oauth-token-a")

	setupTokenA := &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token-a"}}
	setupTokenADuplicate := &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token-a"}}
	setupTokenB := &Account{ID: 32, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "setup-token-b"}}
	setupNamespace := codexAccountIdentityNamespace(setupTokenA)
	require.NotEmpty(t, setupNamespace)
	require.NotContains(t, setupNamespace, "setup-token-a")
	require.Equal(t, setupNamespace, codexAccountIdentityNamespace(setupTokenADuplicate))
	require.NotEqual(t, setupNamespace, codexAccountIdentityNamespace(setupTokenB))
}

func TestCodexAccountIdentitySourceResolvesShadowAndOverwritesFailoverContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	parentID := int64(11)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": "team-account",
		"chatgpt_user_id":    "user-1",
	}}
	shadow := &Account{ID: 111, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	service := &OpenAIGatewayService{accountRepo: &codexAccountIdentityRepoStub{account: parent}}

	resolved, err := service.prepareCodexAccountIdentitySource(context.Background(), c, shadow)
	require.NoError(t, err)
	require.Same(t, parent, resolved)
	require.Same(t, parent, codexAccountIdentitySource(c, shadow))

	req, err := service.buildUpstreamRequest(
		context.Background(), c, shadow,
		[]byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`),
		"token", true, "client-session", true,
	)
	require.NoError(t, err)
	require.Equal(t, scopeCodexAccountIdentityValue(parent, 0, "session", "client-session"), req.Header.Get("session-id"))

	next := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"chatgpt_account_id": "other-account",
		"chatgpt_user_id":    "user-2",
	}}
	resolved, err = service.prepareCodexAccountIdentitySource(context.Background(), c, next)
	require.NoError(t, err)
	require.Same(t, next, resolved)
	require.Same(t, next, codexAccountIdentitySource(c, shadow))
}

func TestBuildOpenAIWSHeadersNamespacesCodexIdentityByOAuthAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("api_key_id", int64(77))
	c.Request.Header.Set("x-codex-installation-id", "client-installation")
	c.Request.Header.Set("thread-id", "client-thread")
	c.Request.Header.Set("x-codex-window-id", "client-window")
	c.Request.Header.Set("x-client-request-id", "client-request")

	account11 := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-account-11"}}
	account19 := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "chatgpt-account-19"}}
	service := &OpenAIGatewayService{}
	build := func(account *Account) http.Header {
		headers, _, err := service.buildOpenAIWSHeaders(
			context.Background(), c, account, "token",
			OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
			true, "", "", "client-session", "", "",
		)
		require.NoError(t, err)
		return headers
	}

	first := build(account11)
	firstAgain := build(account11)
	second := build(account19)
	for _, header := range []string{"session-id", "x-codex-installation-id", "thread-id", "x-codex-window-id", "x-client-request-id"} {
		require.NotEmpty(t, first.Get(header), header)
		require.Equal(t, first.Get(header), firstAgain.Get(header), header)
		require.NotEqual(t, first.Get(header), second.Get(header), header)
	}

	httpRequest, err := service.buildUpstreamRequest(
		context.Background(), c, account11,
		[]byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`),
		"token", true, "client-session", true,
	)
	require.NoError(t, err)
	require.Equal(t, httpRequest.Header.Get("session-id"), first.Get("session-id"), "HTTP and WS must derive the same identity from the raw client key")
}

func TestBuildUpstreamRequestNamespacesCodexIdentityByOAuthAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"client-session"}`)

	build := func(accountID int64, chatgptAccountID string) http.Header {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Set("api_key", &APIKey{ID: 77})
		c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.0")
		c.Request.Header.Set("x-codex-installation-id", "client-installation")
		c.Request.Header.Set("x-codex-window-id", "client-window")
		c.Request.Header.Set("session-id", "client-session")
		c.Request.Header.Set("thread-id", "client-thread")
		c.Request.Header.Set("x-client-request-id", "client-request")
		c.Request.Header.Set("x-codex-turn-metadata", `{"installation_id":"client-installation","session_id":"client-session","thread_id":"client-thread","turn_id":"client-turn","window_id":"client-window"}`)

		account := &Account{
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"chatgpt_account_id": chatgptAccountID,
			},
		}
		req, err := svc.buildUpstreamRequest(
			context.Background(), c, account, body, "oauth-token", true, "client-session", true,
		)
		require.NoError(t, err)
		return req.Header
	}

	first := build(11, "chatgpt-account-11")
	firstAgain := build(11, "chatgpt-account-11")
	second := build(19, "chatgpt-account-19")

	identityHeaders := []string{
		"x-codex-installation-id",
		"x-codex-window-id",
		"session-id",
		"session_id",
		"conversation_id",
		"thread-id",
		"x-client-request-id",
		"x-codex-turn-metadata",
	}
	checked := 0
	for _, header := range identityHeaders {
		if first.Get(header) == "" && second.Get(header) == "" {
			continue
		}
		checked++
		require.NotEmpty(t, first.Get(header), header)
		require.Equal(t, first.Get(header), firstAgain.Get(header), "same account must retain stable identity: %s", header)
		require.NotEqual(t, first.Get(header), second.Get(header), "account failover must rotate upstream identity: %s", header)
	}
	require.GreaterOrEqual(t, checked, 5, "test must exercise the real outbound identity surface")
}

func TestBuildUpstreamRequestPreservesCodexRootSessionIdentityEquality(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		rawID     = "019fbafc-59e6-7d62-8ec6-38ddc077db6f"
		rawTurnID = "01a15cec-3900-72c2-bcbd-939ff4dd2ed5"
		rawWindow = rawID + ":47"
	)
	body := []byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"` + rawID + `","client_metadata":{"session_id":"` + rawID + `","thread_id":"` + rawID + `","turn_id":"` + rawTurnID + `","root_turn_id":"` + rawTurnID + `","x-codex-window-id":"` + rawWindow + `","x-codex-turn-metadata":"{\"session_id\":\"` + rawID + `\",\"thread_id\":\"` + rawID + `\",\"turn_id\":\"` + rawTurnID + `\",\"root_turn_id\":\"` + rawTurnID + `\",\"window_id\":\"` + rawWindow + `\"}"}}`)
	account := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-account-11",
			"chatgpt_user_id":    "user-11",
		},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 77})
	c.Request.Header.Set("session-id", rawID)
	c.Request.Header.Set("thread-id", rawID)
	c.Request.Header.Set("x-client-request-id", rawID)
	c.Request.Header.Set("x-codex-window-id", rawWindow)
	c.Request.Header.Set("x-codex-turn-metadata", `{"session_id":"`+rawID+`","thread_id":"`+rawID+`","turn_id":"`+rawTurnID+`","root_turn_id":"`+rawTurnID+`","window_id":"`+rawWindow+`"}`)

	rewrittenBody, changed, err := applyCodexAccountIdentityClientMetadataRaw(body, account, 77)
	require.NoError(t, err)
	require.True(t, changed)

	svc := &OpenAIGatewayService{}
	req, err := svc.buildUpstreamRequest(context.Background(), c, account, rewrittenBody, "oauth-token", true, rawID, true)
	require.NoError(t, err)

	sessionID := req.Header.Get("session-id")
	threadID := req.Header.Get("thread-id")
	requestID := req.Header.Get("x-client-request-id")
	require.NotEmpty(t, sessionID)
	require.NotEqual(t, rawID, sessionID)
	require.Equal(t, sessionID, threadID, "Codex CLI uses the same id for session-id and thread-id on root sessions")
	require.Equal(t, threadID, requestID, "Codex CLI sends x-client-request-id equal to thread-id")

	windowID := req.Header.Get("x-codex-window-id")
	require.Equal(t, threadID+":47", windowID, "window_id must keep Codex's thread-id:window-number shape")
	require.NotContains(t, windowID, rawID)

	var headerMetadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(req.Header.Get("x-codex-turn-metadata")), &headerMetadata))
	require.Equal(t, threadID, headerMetadata["thread_id"])
	require.Equal(t, headerMetadata["turn_id"], headerMetadata["root_turn_id"], "root sessions keep root_turn_id equal to turn_id")
	require.Equal(t, windowID, headerMetadata["window_id"])
	rootTurnID, ok := headerMetadata["root_turn_id"].(string)
	require.True(t, ok)
	require.NotContains(t, rootTurnID, rawTurnID)

	upstreamBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, sessionID, gjson.GetBytes(upstreamBody, "prompt_cache_key").String())
	require.Equal(t, sessionID, gjson.GetBytes(upstreamBody, "client_metadata.session_id").String())
	require.Equal(t, threadID, gjson.GetBytes(upstreamBody, "client_metadata.thread_id").String())
	require.Equal(t, headerMetadata["turn_id"], gjson.GetBytes(upstreamBody, "client_metadata.turn_id").String())
	require.Equal(t, headerMetadata["root_turn_id"], gjson.GetBytes(upstreamBody, "client_metadata.root_turn_id").String())
	require.Equal(t, windowID, gjson.GetBytes(upstreamBody, "client_metadata.x-codex-window-id").String())
	require.NotContains(t, string(upstreamBody), rawTurnID)
	var embedded map[string]any
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(upstreamBody, "client_metadata.x-codex-turn-metadata").String()), &embedded))
	require.Equal(t, sessionID, embedded["session_id"])
	require.Equal(t, threadID, embedded["thread_id"])
	require.Equal(t, headerMetadata["turn_id"], embedded["turn_id"])
	require.Equal(t, headerMetadata["root_turn_id"], embedded["root_turn_id"])
	require.Equal(t, windowID, embedded["window_id"])
}

func TestCodexAccountIdentityPreservesTurnHierarchyAndWindowNumber(t *testing.T) {
	const (
		childThread = "01a11084-9fb2-7372-814b-091f9adf08a2"
		childTurn   = "01a15d17-bbbd-7933-983b-6ca5b55dd0ae"
		parentTurn  = "01a15cec-3900-72c2-bcbd-939ff4dd2ed5"
	)
	account := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-account-11",
			"chatgpt_user_id":    "user-11",
		},
	}

	values := map[string]any{
		"thread_id":      childThread,
		"turn_id":        childTurn,
		"root_turn_id":   parentTurn,
		"parent_turn_id": parentTurn,
		"window_id":      childThread + ":3",
	}
	require.True(t, applyCodexAccountIdentityFields(values, account, 77))

	scopedThread := scopeCodexAccountIdentityValue(account, 77, codexSessionIdentityKind, childThread)
	scopedChildTurn := scopeCodexAccountIdentityValue(account, 77, "turn", childTurn)
	scopedParentTurn := scopeCodexAccountIdentityValue(account, 77, "turn", parentTurn)
	require.Equal(t, scopedThread, values["thread_id"])
	require.Equal(t, scopedChildTurn, values["turn_id"])
	require.Equal(t, scopedParentTurn, values["root_turn_id"])
	require.Equal(t, scopedParentTurn, values["parent_turn_id"])
	require.NotEqual(t, values["turn_id"], values["root_turn_id"], "a subagent keeps its own turn distinct from the ancestor turn")
	require.Equal(t, scopedThread+":3", values["window_id"])

	// Non-canonical window values keep the legacy whole-value scoping.
	legacy := map[string]any{"window_id": "opaque-window"}
	require.True(t, applyCodexAccountIdentityFields(legacy, account, 77))
	require.Equal(t, scopeCodexAccountIdentityValue(account, 77, codexWindowIdentityKind, "opaque-window"), legacy["window_id"])
}

func TestBuildUpstreamRequestKeepsSubagentSessionAndThreadDistinct(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		rootSession = "019fbafc-59e6-7d62-8ec6-38ddc077db6f"
		childThread = "01a11084-9fb2-7372-814b-091f9adf08a2"
	)
	body := []byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"` + rootSession + `","client_metadata":{"session_id":"` + rootSession + `","thread_id":"` + childThread + `"}}`)
	account := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-account-11",
			"chatgpt_user_id":    "user-11",
		},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 77})
	c.Request.Header.Set("session-id", rootSession)
	c.Request.Header.Set("thread-id", childThread)
	c.Request.Header.Set("x-client-request-id", childThread)

	svc := &OpenAIGatewayService{}
	req, err := svc.buildUpstreamRequest(context.Background(), c, account, body, "oauth-token", true, rootSession, true)
	require.NoError(t, err)

	require.NotEqual(t, req.Header.Get("session-id"), req.Header.Get("thread-id"))
	require.Equal(t, req.Header.Get("thread-id"), req.Header.Get("x-client-request-id"))
}

func TestBuildUpstreamRequestDerivesMissingClientRequestIDFromThread(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const rawID = "019fbafc-59e6-7d62-8ec6-38ddc077db6f"
	body := []byte(`{"model":"gpt-5.6-codex","stream":true,"prompt_cache_key":"` + rawID + `","client_metadata":{"session_id":"` + rawID + `","thread_id":"` + rawID + `"}}`)
	account := &Account{
		ID:       11,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-account-11",
			"chatgpt_user_id":    "user-11",
		},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 77})
	c.Request.Header.Set("session-id", rawID)
	c.Request.Header.Set("thread-id", rawID)

	svc := &OpenAIGatewayService{}
	req, err := svc.buildUpstreamRequest(context.Background(), c, account, body, "oauth-token", true, rawID, true)
	require.NoError(t, err)

	require.Equal(t, req.Header.Get("thread-id"), req.Header.Get("x-client-request-id"))
}

func TestCodexNativeSessionIdentityUsesOfficialHeadersAndIsolatesAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-codex","prompt_cache_key":"downstream-session","client_metadata":{"session_id":"downstream-session","thread_id":"downstream-thread"}}`)
	accounts := []*Account{
		{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "upstream-a"}},
		{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "upstream-b"}},
	}

	build := func(account *Account) http.Header {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("session-id", "downstream-session")
		c.Request.Header.Set("thread-id", "downstream-thread")
		c.Request.Header.Set("x-client-request-id", "downstream-request")
		stageCodexNativeSessionIdentity(c, account, 77, "downstream-session", body)
		identity := resolveCodexNativeSessionIdentity(c, account, 77, "downstream-session", body)
		headers := make(http.Header)
		applyCodexNativeSessionHeaders(headers, identity)
		return headers
	}

	first := build(accounts[0])
	firstAgain := build(accounts[0])
	second := build(accounts[1])
	for _, name := range []string{"session-id", "thread-id", "x-client-request-id"} {
		require.NotEmpty(t, first.Get(name), name)
		require.Equal(t, first.Get(name), firstAgain.Get(name), name)
		require.NotEqual(t, first.Get(name), second.Get(name), name)
		require.NotContains(t, first.Get(name), "sub2api", name)
		require.NotContains(t, second.Get(name), "sub2api", name)
	}
	require.Empty(t, first.Get("conversation_id"))
	require.Empty(t, first.Get("session_id"))
	_, err := uuid.Parse(first.Get("session-id"))
	require.NoError(t, err)
}

func TestCodexNativeSessionIdentityHashesSub2APIInputWithoutNamespace(t *testing.T) {
	account := &Account{ID: 44, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	identity := stageCodexNativeSessionIdentity(c, account, 0, "sub2api:downstream-session", nil)
	require.NotNil(t, identity)
	for _, value := range []string{identity.SessionID, identity.ThreadID, identity.RequestID} {
		require.NotEmpty(t, value)
		require.NotContains(t, value, "sub2api")
	}
}
