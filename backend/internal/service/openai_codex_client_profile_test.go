package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const capturedLinuxTUIUserAgent = "codex-tui/0.169.0 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.169.0)"
const capturedDesktopUserAgent = "Codex Desktop/0.169.0-alpha.11.1 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.1111.21308)"

func withNativeCodexIdentitySettings(t *testing.T) {
	t.Helper()
	codexCanonicalUAMu.RLock()
	previousResolver := codexCanonicalUAResolver
	codexCanonicalUAMu.RUnlock()
	previousEnforcement := codexIdentityEnforcement.Load()
	SetCodexIdentityEnforcementEnabled(true)
	// Native versions and Desktop build trailers must be independent of the
	// synchronized version used for compatibility probes and missing clients.
	SetCodexCanonicalUserAgentResolver(func() string {
		return "codex_cli_rs/0.222.0" + codexCLIUserAgentSuffix
	})
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(previousResolver)
		SetCodexIdentityEnforcementEnabled(previousEnforcement)
	})
}

func nativeCodexIdentityContext(ua, originator string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", ua)
	c.Request.Header.Set("originator", originator)
	c.Request.Header.Set("session-id", "019a0234-5678-7abc-8def-0123456789ab")
	c.Request.Header.Set("thread-id", c.GetHeader("session-id"))
	c.Request.Header.Set("x-client-request-id", c.GetHeader("session-id"))
	return c
}

func TestNativeCodexUserAgentPreservesOfficialClientIdentity(t *testing.T) {
	withNativeCodexIdentitySettings(t)
	for _, tc := range []struct {
		name       string
		ua         string
		originator string
	}{
		{"captured_linux_tui", capturedLinuxTUIUserAgent, "codex-tui"},
		{"captured_windows_desktop", capturedDesktopUserAgent, "Codex Desktop"},
		{"future_desktop_build", "Codex Desktop/0.171.0-alpha.2.1 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.1120.10000)", "Codex Desktop"},
		{"windows_tui_unknown_terminal", "codex-tui/0.169.0 (Windows 10.0.26200; x86_64) unknown (codex-tui; 0.169.0)", "codex-tui"},
		{"linux_exec", "codex_exec/0.169.0 (Ubuntu 24.4.0; x86_64) xterm-256color (codex_exec; 0.169.0)", "codex_exec"},
		{"macos_desktop", "Codex Desktop/0.169.0-alpha.11.1 (Mac OS 15.0; arm64) unknown (Codex Desktop; 26.1111.21308)", "Codex Desktop"},
		{"library_cli", "codex_cli_rs/0.169.0 (Ubuntu 24.4.0; x86_64) xterm-256color", "codex_cli_rs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := nativeCodexIdentityContext(tc.ua, "mismatched-originator")
			// Remote workspace paths are not the client's operating system.
			body := []byte(`{"client_metadata":{"x-codex-turn-metadata":"{\"workspaces\":{\"D:\\\\Projects\\\\demo\":{}}}"}}`)
			profile := resolveCodexClientProfile(c, body, false)
			header := c.Request.Header.Clone()
			header.Set("Version", "9.9.9")
			enforceCodexIdentityHeadersWithProfile(header, "", profile)
			require.Equal(t, tc.ua, header.Get("User-Agent"))
			require.Equal(t, tc.originator, header.Get("originator"))
			require.Empty(t, header.Get("Version"))
			wsProfile := resolveCodexClientProfileFromTurnMetadata(c, `{"client_type":"desktop_app"}`, false)
			require.Equal(t, profile.userAgent, wsProfile.userAgent)
			require.Equal(t, profile.originator, wsProfile.originator)
		})
	}
}

func TestNativeCodexUserAgentFallbacksAndExplicitOverrides(t *testing.T) {
	withNativeCodexIdentitySettings(t)
	t.Run("missing_desktop_ua_uses_current_capture", func(t *testing.T) {
		c := nativeCodexIdentityContext("", "Codex Desktop")
		body := []byte(`{"client_metadata":{"x-codex-turn-metadata":"{\"workspaces\":{\"D:\\\\Projects\\\\demo\":{}}}"}}`)
		profile := resolveCodexClientProfile(c, body, false)
		require.Equal(t, capturedDesktopUserAgent, profile.userAgent)
	})
	for _, name := range []string{"codex-tui", "codex_exec"} {
		t.Run("missing_ua_"+name, func(t *testing.T) {
			c := nativeCodexIdentityContext("", name)
			profile := resolveCodexClientProfile(c, nil, false)
			require.Equal(t, name, profile.originator)
			require.Equal(t, name+"/0.222.0"+codexCLIUserAgentSuffix+" ("+name+"; 0.222.0)", profile.userAgent)
		})
	}
	t.Run("force_cli_keeps_explicit_compatibility_policy", func(t *testing.T) {
		c := nativeCodexIdentityContext(capturedDesktopUserAgent, "Codex Desktop")
		profile := resolveCodexClientProfile(c, nil, true)
		require.Equal(t, openai.CodexCLIOriginator, profile.originator)
		require.Equal(t, buildCodexCLIUserAgent("0.222.0"), profile.userAgent)
	})
	t.Run("bare_tui_identity_uses_tui_fallback", func(t *testing.T) {
		c := nativeCodexIdentityContext("codex-tui/0.169.0", "codex_cli_rs")
		profile := resolveCodexClientProfile(c, nil, false)
		require.Equal(t, "codex-tui", profile.originator)
		require.Equal(t, "codex-tui/0.222.0"+codexCLIUserAgentSuffix+" (codex-tui; 0.222.0)", profile.userAgent)
	})
	t.Run("account_tui_override_keeps_identity_and_version_sync", func(t *testing.T) {
		c := nativeCodexIdentityContext(capturedDesktopUserAgent, "Codex Desktop")
		header := c.Request.Header.Clone()
		enforceCodexIdentityHeadersWithProfile(header, capturedLinuxTUIUserAgent, resolveCodexClientProfile(c, nil, false))
		require.Equal(t, "codex-tui/0.222.0"+codexCLIUserAgentSuffix+" (codex-tui; 0.222.0)", header.Get("User-Agent"))
		require.Equal(t, "codex-tui", header.Get("originator"))
		require.Empty(t, header.Get("Version"))
	})
	t.Run("account_override_preserves_desktop_build_and_omits_version", func(t *testing.T) {
		c := nativeCodexIdentityContext(capturedLinuxTUIUserAgent, "codex-tui")
		header := c.Request.Header.Clone()
		header.Set("Version", "9.9.9")
		enforceCodexIdentityHeadersWithProfile(header, capturedDesktopUserAgent, resolveCodexClientProfile(c, nil, false))
		require.Equal(t, capturedDesktopUserAgent, header.Get("User-Agent"))
		require.Equal(t, "Codex Desktop", header.Get("originator"))
		require.Empty(t, header.Get("Version"))
	})
	for _, invalid := range []string{"luna/1.0", "codex-tui/not-a-version (Ubuntu 24.4.0; x86_64) xterm", "codex-tui/0.169.0\r\nX-Injected: 1"} {
		t.Run("invalid_ua_"+strings.ReplaceAll(invalid, "\r\n", "_"), func(t *testing.T) {
			_, ok := codexClientProfileFromUserAgent(invalid)
			require.False(t, ok)
			c := nativeCodexIdentityContext(invalid, "Codex Desktop")
			profile := resolveCodexClientProfile(c, nil, false)
			require.Equal(t, capturedDesktopUserAgent, profile.userAgent)
		})
	}
}

func TestNativeCodexUserAgentHTTPAndWebSocketHeaderParity(t *testing.T) {
	withNativeCodexIdentitySettings(t)
	for _, ua := range []string{capturedLinuxTUIUserAgent, capturedDesktopUserAgent, "codex_exec/0.169.0 (Ubuntu 24.4.0; x86_64) xterm-256color (codex_exec; 0.169.0)"} {
		originator, _, ok := openai.PairCodexClientIdentity(ua)
		require.True(t, ok)
		for _, transport := range []string{"http", "passthrough", "websocket"} {
			t.Run(originator+"_"+transport, func(t *testing.T) {
				c := nativeCodexIdentityContext(ua, "codex_cli_rs")
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"}}
				body := []byte(`{"model":"gpt-5.4","stream":true,"input":[]}`)
				var headers http.Header
				var err error
				switch transport {
				case "http":
					var request *http.Request
					request, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "test-token", true, "", true)
					if request != nil {
						headers = request.Header
					}
				case "passthrough":
					var request *http.Request
					request, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
					if request != nil {
						headers = request.Header
					}
				case "websocket":
					headers, _, err = svc.buildOpenAIWSHeaders(context.Background(), c, account, "test-token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "gpt-5.4", "")
				}
				require.NoError(t, err)
				require.Equal(t, ua, headers.Get("User-Agent"))
				require.Equal(t, originator, headers.Get("originator"))
				require.Empty(t, headers.Get("Version"))
				require.NotEmpty(t, headers.Get("session-id"))
				require.Equal(t, headers.Get("session-id"), headers.Get("thread-id"))
				require.Equal(t, headers.Get("thread-id"), headers.Get("x-client-request-id"))
			})
		}
	}
}
