package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexClientProfileDetection(t *testing.T) {
	tests := []struct {
		name       string
		userAgent  string
		originator string
		metadata   string
		want       codexClientProfileKind
	}{
		{
			name:       "official desktop ua",
			userAgent:  codexWindowsDesktopUserAgent,
			originator: codexWindowsDesktopOriginator,
			want:       codexClientProfileWindowsDesktop,
		},
		{
			name:       "ubuntu cli",
			userAgent:  codexCLIUserAgent,
			originator: openai.CodexDefaultOriginator,
			want:       codexClientProfileUbuntuCLI,
		},
		{
			name:       "windows cli",
			userAgent:  openai.CodexDefaultOriginator + "/0.146.0" + codexWindowsCLIUserAgentSuffix,
			originator: openai.CodexDefaultOriginator,
			want:       codexClientProfileWindowsCLI,
		},
		{
			name:     "desktop metadata",
			metadata: `{"client_type":"desktop_app","workspaces":{"D:\\Projects\\demo":{}}}`,
			want:     codexClientProfileWindowsDesktop,
		},
		{
			name:     "windows workspace path",
			metadata: `{"workspaces":{"E:\\Projects\\demo":{}}}`,
			want:     codexClientProfileWindowsCLI,
		},
		{
			name:     "workspace kind alone is inconclusive",
			metadata: `{"workspace_kind":"project"}`,
			want:     codexClientProfileUbuntuCLI,
		},
		{
			name:       "unknown falls back to ubuntu",
			userAgent:  "luna/1.2.0",
			originator: "luna",
			want:       codexClientProfileUbuntuCLI,
		},
		{
			name:      "windows browser falls back to ubuntu",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0 Safari/537.36",
			want:      codexClientProfileUbuntuCLI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, detectCodexClientProfileKind(tt.userAgent, tt.originator, tt.metadata))
		})
	}
}

func TestCodexClientProfileNativeIdentityOmitsVersion(t *testing.T) {
	old := codexIdentityEnforcement.Load()
	codexIdentityEnforcement.Store(true)
	defer codexIdentityEnforcement.Store(old)

	for _, kind := range []codexClientProfileKind{
		codexClientProfileUbuntuCLI,
		codexClientProfileWindowsCLI,
		codexClientProfileWindowsDesktop,
	} {
		h := http.Header{}
		h.Set("originator", "placeholder")
		h.Set("version", "9.9.9")
		enforceCodexIdentityHeadersWithProfile(h, "", codexClientProfileForKind(kind))
		require.Empty(t, h.Get("version"), "native inference must not send Version")
		require.NotEmpty(t, h.Get("user-agent"))
		require.NotEmpty(t, h.Get("originator"))
	}
}

func TestCodexClientProfileKeepsExplicitAccountUA(t *testing.T) {
	old := codexIdentityEnforcement.Load()
	codexIdentityEnforcement.Store(true)
	defer codexIdentityEnforcement.Store(old)

	h := http.Header{}
	h.Set("originator", "placeholder")
	enforceCodexIdentityHeadersWithProfile(h, "codex_cli_rs/0.150.0 (Ubuntu 22.4.0; x86_64) xterm-256color", codexClientProfileForKind(codexClientProfileWindowsDesktop))
	require.Equal(t, openai.CodexDefaultOriginator, h.Get("originator"))
	require.Contains(t, h.Get("user-agent"), "Ubuntu 22.4.0; x86_64")
	require.Equal(t, codexCLIVersion, h.Get("version"))
}

func TestOpenAIWSEffectiveTurnMetadataPrefersPayloadThenHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

	payload := []byte(`{"client_metadata":{"x-codex-turn-metadata":"{\"client_type\":\"desktop_app\"}"}}`)
	require.Equal(t, `{"client_type":"desktop_app"}`, openAIWSEffectiveTurnMetadata(c, payload))

	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"client_type":"cli"}`)
	require.Equal(t, `{"client_type":"desktop_app"}`, openAIWSEffectiveTurnMetadata(c, payload))

	decoded := map[string]any{
		"client_metadata": map[string]any{
			openAIWSTurnMetadataHeader: `{"client_type":"desktop_app"}`,
		},
	}
	require.Equal(t, `{"client_type":"desktop_app"}`, openAIWSEffectiveTurnMetadataFromMap(nil, decoded))
	require.Equal(t, `{"client_type":"desktop_app"}`, openAIWSEffectiveTurnMetadataFromMap(c, decoded))
}

func TestCodexClientProfileUsesCanonicalBodyMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "luna/1.2.0")
	c.Request.Header.Set("originator", "luna")
	body := []byte(`{"client_metadata":{"x-codex-turn-metadata":"{\"client_type\":\"desktop_app\"}"}}`)

	profile := resolveCodexClientProfile(c, body, false)
	require.Equal(t, codexClientProfileWindowsDesktop, profile.kind)
	require.True(t, isCodexOfficialClientForRequest(c, body))

	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"client_type":"cli"}`)
	profile = resolveCodexClientProfile(c, body, false)
	require.Equal(t, codexClientProfileWindowsDesktop, profile.kind, "body metadata is canonical")
}

func TestDeriveStableUUIDv7PreservesSourceTimestamp(t *testing.T) {
	source := "01a15d78-00a4-70d2-ac6c-d45db27b9ee8"
	derived := deriveStableUUIDv7("seed-value", source)
	parsed, err := uuid.Parse(derived)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), parsed.Version())

	sourceHex := strings.ReplaceAll(source, "-", "")
	derivedHex := strings.ReplaceAll(derived, "-", "")
	require.Equal(t, sourceHex[:12], derivedHex[:12], "48-bit timestamp must be preserved")
	require.Equal(t, byte(0x80), parsed[8]&0xc0, "RFC 4122 variant bits")
}

func TestDeriveStableUUIDv7IsDeterministicAndVersioned(t *testing.T) {
	a := deriveStableUUIDv7("same-seed", "")
	b := deriveStableUUIDv7("same-seed", "")
	require.Equal(t, a, b)
	parsed, err := uuid.Parse(a)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), parsed.Version())
	require.NotEqual(t, a, deriveStableUUIDv7("other-seed", ""))
}

func TestMarshalCodexResponsesJSONUsesOfficialOrder(t *testing.T) {
	body := map[string]any{
		"client_metadata": map[string]any{
			"turn_id":    "turn",
			"thread_id":  "thread",
			"session_id": "session",
		},
		"input": []any{
			map[string]any{
				"content": []any{
					map[string]any{"text": "hi", "type": "input_text"},
				},
				"role": "user",
				"type": "message",
			},
		},
		"stream": true,
		"model":  "gpt-5.2",
	}

	raw, err := marshalCodexResponsesJSON(body)
	require.NoError(t, err)
	require.Equal(t,
		`{"model":"gpt-5.2","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"client_metadata":{"session_id":"session","thread_id":"thread","turn_id":"turn"}}`,
		string(raw),
	)
}

func TestMarshalCodexTurnMetadataUsesOfficialOrder(t *testing.T) {
	metadata := map[string]any{
		"client_type":             "desktop_app",
		"session_id":              "session",
		"installation_id":         "install",
		"turn_id":                 "turn",
		"window_id":               "thread:0",
		"thread_id":               "thread",
		"turn_started_at_unix_ms": int64(1792569645288),
		"workspaces": map[string]any{
			"E:\\Projects\\demo": map[string]any{
				"has_changes":            true,
				"latest_git_commit_hash": "abc",
				"associated_remote_urls": map[string]any{"origin": "git@example/demo.git"},
			},
		},
	}

	raw, err := marshalCodexTurnMetadata(metadata)
	require.NoError(t, err)
	text := string(raw)
	require.True(t, strings.HasPrefix(text, `{"installation_id":"install","session_id":"session","thread_id":"thread","turn_id":"turn","window_id":"thread:0","workspaces":`), text)
	require.Contains(t, text, `"workspaces":{"E:\\Projects\\demo":{"associated_remote_urls":{"origin":"git@example/demo.git"},"latest_git_commit_hash":"abc","has_changes":true}}`)
	require.True(t, strings.HasSuffix(text, `"turn_started_at_unix_ms":1792569645288,"client_type":"desktop_app"}`), text)
}

func TestCodexFingerprintKeepsTurnAndThreadRelationships(t *testing.T) {
	ids := &codexFingerprintIDs{
		mode:           codexFingerprintSession,
		installationID: "install-new",
		sessionID:      "session-new",
		threadID:       "thread-new",
		turnID:         "turn-new",
		windowID:       "thread-new:0",
	}
	const embedded = `{"session_id":"session-old","thread_id":"thread-old","turn_id":"turn-old","root_turn_id":"turn-old","parent_turn_id":"turn-old","parent_thread_id":"thread-old","forked_from_thread_id":"thread-old"}`
	metadata := map[string]any{
		"session_id":            "session-old",
		"thread_id":             "thread-old",
		"turn_id":               "turn-old",
		"root_turn_id":          "turn-old",
		"parent_turn_id":        "turn-old",
		"parent_thread_id":      "thread-old",
		"forked_from_thread_id": "thread-old",
		"x-codex-turn-metadata": embedded,
	}
	require.True(t, applyCodexFingerprintToClientMetadataMap(metadata, ids))
	require.Equal(t, "session-new", metadata["session_id"])
	require.Equal(t, "thread-new", metadata["thread_id"])
	require.Equal(t, "turn-new", metadata["turn_id"])
	require.Equal(t, "turn-new", metadata["root_turn_id"])
	require.Equal(t, "turn-new", metadata["parent_turn_id"])
	require.Equal(t, "thread-new", metadata["parent_thread_id"])
	require.Equal(t, "thread-new", metadata["forked_from_thread_id"])

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(metadata["x-codex-turn-metadata"].(string)), &decoded))
	require.Equal(t, "turn-new", decoded["root_turn_id"])
	require.Equal(t, "turn-new", decoded["parent_turn_id"])
	require.Equal(t, "thread-new", decoded["parent_thread_id"])
	require.Equal(t, "thread-new", decoded["forked_from_thread_id"])
}

func TestFilterCodexInputPreservesOfficialNativeIDs(t *testing.T) {
	input := []any{
		map[string]any{
			"type":              "reasoning",
			"id":                "rs_reasoning",
			"summary":           []any{},
			"encrypted_content": "encrypted",
		},
		map[string]any{
			"type":    "custom_tool_call",
			"id":      "ctc_call",
			"call_id": "call_1",
			"name":    "shell",
			"input":   "ls",
		},
		map[string]any{
			"type":    "custom_tool_call_output",
			"id":      "ctc_output",
			"call_id": "call_1",
			"output":  "ok",
		},
		map[string]any{
			"type":      "function_call",
			"id":        "fc_call",
			"call_id":   "call_2",
			"name":      "lookup",
			"arguments": "{}",
		},
		map[string]any{
			"type":    "function_call_output",
			"id":      "fc_output",
			"call_id": "call_2",
			"output":  "ok",
		},
	}

	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
		PreserveCallIDs:    true,
		PreserveItemIDs:    true,
	})
	require.Len(t, filtered, len(input))
	require.Equal(t, "rs_reasoning", filtered[0].(map[string]any)["id"])
	require.Equal(t, "ctc_call", filtered[1].(map[string]any)["id"])
	require.Equal(t, "call_1", filtered[1].(map[string]any)["call_id"])
	require.Equal(t, "ctc_output", filtered[2].(map[string]any)["id"])
	require.Equal(t, "call_1", filtered[2].(map[string]any)["call_id"])
	require.Equal(t, "fc_call", filtered[3].(map[string]any)["id"])
	require.Equal(t, "call_2", filtered[3].(map[string]any)["call_id"])
	require.Equal(t, "fc_output", filtered[4].(map[string]any)["id"])
	require.Equal(t, "call_2", filtered[4].(map[string]any)["call_id"])
}

func TestFilterCodexInputStripsInvalidNativeIDs(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "custom_tool_call",
			"id":      "fc_wrong_namespace",
			"call_id": "fc_wrong_call",
			"name":    "shell",
			"input":   "ls",
		},
	}
	filtered := filterCodexInputWithOptions(input, codexInputFilterOptions{
		PreserveReferences: true,
		PreserveCallIDs:    true,
		PreserveItemIDs:    true,
	})
	item := filtered[0].(map[string]any)
	require.NotContains(t, item, "id", "invalid item id must be removed")
	require.Equal(t, "ctc_wrong_call", item["call_id"], "invalid call id must be normalized")
}

func TestApplyCodexOAuthTransformSkipsDefaultInstructionsForNative(t *testing.T) {
	body := map[string]any{
		"model":  "gpt-5.2",
		"stream": true,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		},
	}
	result := applyCodexOAuthTransformWithOptions(body, codexOAuthTransformOptions{
		IsCodexCLI:              true,
		SkipDefaultInstructions: true,
		PreserveToolCallIDs:     true,
		PreserveItemIDs:         true,
	})
	require.NoError(t, result.Error)
	require.NotContains(t, body, "instructions")

	withDefault := map[string]any{
		"model":  "gpt-5.2",
		"stream": true,
		"input":  []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}}},
	}
	result = applyCodexOAuthTransformWithOptions(withDefault, codexOAuthTransformOptions{IsCodexCLI: true})
	require.NoError(t, result.Error)
	require.NotEmpty(t, withDefault["instructions"])
}

func TestForwardOfficialCodexDoesNotSynthesizeInstructions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.2","stream":true,"store":true,"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"development guidance"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("User-Agent", codexWindowsDesktopUserAgent)
	c.Request.Header.Set("originator", codexWindowsDesktopOriginator)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          1,
		Name:        "oauth-native",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
		Status:         StatusActive,
		Schedulable:    true,
		RateMultiplier: f64p(1),
	}

	_, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists(), "native Codex request must not gain synthetic instructions")
	require.Equal(t, codexWindowsDesktopUserAgent, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, codexWindowsDesktopOriginator, upstream.lastReq.Header.Get("originator"))
	require.Empty(t, upstream.lastReq.Header.Get("Version"))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(upstream.lastBody, &decoded))
	require.Equal(t, "message", gjson.GetBytes(upstream.lastBody, "input.0.type").String())
}
