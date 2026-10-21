package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Fixed Codex personas used for native OAuth inference. The downstream client
// family is selected from the request headers and the turn metadata body. A
// client we cannot identify falls back to the Ubuntu CLI persona.
type codexClientProfileKind int

const (
	codexClientProfileUbuntuCLI codexClientProfileKind = iota
	codexClientProfileWindowsDesktop
	codexClientProfileWindowsCLI
)

const (
	codexWindowsCLIUserAgentSuffix = " (Windows 10.0.26200; x86_64) WindowsTerminal"
	// Observed Codex Desktop 0.163.0-alpha.10.2 on Windows. The desktop app has
	// a different release train and a `(Codex Desktop; build)` UA trailer, so it
	// must not be rebuilt from the CLI version.
	codexWindowsDesktopUserAgent = "Codex Desktop/0.163.0-alpha.10.2 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.1013.40934)"
)

const (
	codexWindowsDesktopOriginator = "Codex Desktop"
	codexWindowsDesktopVersion    = "0.163.0-alpha.10.2"
)

type codexClientProfile struct {
	kind       codexClientProfileKind
	originator string
	userAgent  string
	version    string
	// sendVersion is false for native inference: current Codex clients do not
	// send the legacy Version header, and upstream only rejects a *present*
	// value below the minimum (codexUpstreamMinVersion). Probes/auth paths
	// still use ensureCodexIdentityHeaders and keep their compatibility value.
	sendVersion bool
}

type codexClientProfileMetadata struct {
	ClientType      string                     `json:"client_type"`
	WorkspaceKind   string                     `json:"workspace_kind"`
	Source          string                     `json:"source"`
	Model           string                     `json:"model"`
	ReasoningEffort string                     `json:"reasoning_effort"`
	Workspaces      map[string]json.RawMessage `json:"workspaces"`
}

// resolveCodexClientProfile selects the outbound persona for a native HTTP
// request. The body is only read for the small client_metadata identity blob;
// it is never rewritten here.
func resolveCodexClientProfile(c *gin.Context, body []byte, forceCLI bool) codexClientProfile {
	if forceCLI {
		return codexClientProfileForKind(codexClientProfileUbuntuCLI)
	}
	metadata := codexTurnMetadataFromBody(body)
	if metadata == "" && c != nil {
		metadata = strings.TrimSpace(c.GetHeader(openAIWSTurnMetadataHeader))
	}
	return resolveCodexClientProfileWithMetadata(c, metadata)
}

// codexTurnMetadataFromBody reads the canonical Codex metadata projection from
// a Responses body. Direct headers are compatibility projections and are used
// only when the body does not carry the canonical value.
func codexTurnMetadataFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	clientMetadata := gjson.GetBytes(body, "client_metadata")
	if !clientMetadata.IsObject() {
		return ""
	}
	return strings.TrimSpace(clientMetadata.Get(openAIWSTurnMetadataHeader).String())
}

// isCodexOfficialClientForRequest extends header detection with the canonical
// body metadata. This keeps native transform decisions aligned with persona
// selection when a downstream client sends metadata only in the body.
func isCodexOfficialClientForRequest(c *gin.Context, body []byte) bool {
	if c != nil && openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) {
		return true
	}
	metadata := codexTurnMetadataFromBody(body)
	if metadata == "" {
		return false
	}
	userAgent := ""
	originator := ""
	if c != nil && c.Request != nil {
		userAgent = c.GetHeader("User-Agent")
		originator = c.GetHeader("originator")
	}
	return detectCodexClientProfileKind(userAgent, originator, metadata) != codexClientProfileUbuntuCLI
}

// resolveCodexClientProfileFromTurnMetadata is the WebSocket equivalent. The
// ingress caller resolves the raw header/body metadata before building the
// handshake headers.
func resolveCodexClientProfileFromTurnMetadata(c *gin.Context, turnMetadata string, forceCLI bool) codexClientProfile {
	if forceCLI {
		return codexClientProfileForKind(codexClientProfileUbuntuCLI)
	}
	return resolveCodexClientProfileWithMetadata(c, turnMetadata)
}

func resolveCodexClientProfileWithMetadata(c *gin.Context, metadataRaw string) codexClientProfile {
	userAgent := ""
	originator := ""
	if c != nil && c.Request != nil {
		userAgent = strings.TrimSpace(c.GetHeader("user-agent"))
		originator = strings.TrimSpace(c.GetHeader("originator"))
	}
	return codexClientProfileForKind(detectCodexClientProfileKind(userAgent, originator, metadataRaw))
}

func detectCodexClientProfileKind(userAgent, originator, metadataRaw string) codexClientProfileKind {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	origin := strings.ToLower(strings.TrimSpace(originator))
	metadata := parseCodexClientProfileMetadata(metadataRaw)

	// Strong body signals win because client_metadata is the canonical Codex
	// snapshot; direct headers are only compatibility projections.
	if strings.Contains(strings.ToLower(metadata.ClientType), "desktop") {
		return codexClientProfileWindowsDesktop
	}
	if codexMetadataHasWindowsWorkspace(metadata) {
		return codexClientProfileWindowsCLI
	}

	desktop := strings.HasPrefix(ua, "codex desktop/") ||
		strings.Contains(ua, "(codex desktop;") ||
		origin == "codex desktop" ||
		origin == "codex_chatgpt_desktop"
	if desktop {
		return codexClientProfileWindowsDesktop
	}

	windows := (strings.HasPrefix(ua, strings.ToLower(openai.CodexDefaultOriginator)+"/") && strings.Contains(ua, "windows")) ||
		strings.Contains(ua, "windowsterminal")
	if windows {
		return codexClientProfileWindowsCLI
	}
	return codexClientProfileUbuntuCLI
}

func parseCodexClientProfileMetadata(raw string) codexClientProfileMetadata {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return codexClientProfileMetadata{}
	}
	var metadata codexClientProfileMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return codexClientProfileMetadata{}
	}
	return metadata
}

func codexMetadataHasWindowsWorkspace(metadata codexClientProfileMetadata) bool {
	for path := range metadata.Workspaces {
		path = strings.TrimSpace(path)
		if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') &&
			((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) {
			return true
		}
	}
	return false
}

func codexClientProfileForKind(kind codexClientProfileKind) codexClientProfile {
	version := strings.TrimSpace(CodexCanonicalClientVersion())
	if version == "" {
		version = codexCLIVersion
	}
	switch kind {
	case codexClientProfileWindowsDesktop:
		return codexClientProfile{
			kind:        kind,
			originator:  codexWindowsDesktopOriginator,
			userAgent:   codexWindowsDesktopUserAgent,
			version:     codexWindowsDesktopVersion,
			sendVersion: false,
		}
	case codexClientProfileWindowsCLI:
		normalized := NormalizeCodexClientVersion(version)
		if normalized == "" {
			normalized = codexCLIVersion
		}
		ua := openai.CodexDefaultOriginator + "/" + normalized + codexWindowsCLIUserAgentSuffix
		return codexClientProfile{
			kind:        kind,
			originator:  openai.CodexDefaultOriginator,
			userAgent:   ua,
			version:     normalized,
			sendVersion: false,
		}
	default:
		return codexClientProfile{
			kind:        codexClientProfileUbuntuCLI,
			originator:  openai.CodexDefaultOriginator,
			userAgent:   buildCodexCLIUserAgent(version),
			version:     version,
			sendVersion: false,
		}
	}
}

// enforceCodexIdentityHeadersWithProfile is the native inference terminal
// identity step. It applies the selected fixed persona unless an account-level
// custom UA is configured; the latter remains an explicit administrator
// override and is paired using the legacy reconstruction path.
func enforceCodexIdentityHeadersWithProfile(h http.Header, overrideUA string, profile codexClientProfile) {
	if h == nil || h.Get("originator") == "" {
		return
	}
	if !codexIdentityEnforcement.Load() {
		pairCodexIdentityHeaders(h)
		return
	}
	if strings.TrimSpace(overrideUA) != "" {
		identity := resolveCodexOutboundIdentity(overrideUA)
		h.Set("user-agent", identity.userAgent)
		h.Set("originator", identity.originator)
		h.Set("version", identity.version)
		return
	}
	if strings.TrimSpace(profile.userAgent) == "" {
		profile = codexClientProfileForKind(codexClientProfileUbuntuCLI)
	}
	h.Set("user-agent", profile.userAgent)
	h.Set("originator", profile.originator)
	if profile.sendVersion && strings.TrimSpace(profile.version) != "" {
		h.Set("version", profile.version)
	} else {
		h.Del("version")
	}
}
