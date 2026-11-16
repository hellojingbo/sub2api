package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Native Codex identity preserves the official client's UA, including its
// engine version, platform and client build trailer. Fixed profiles are only
// used when the UA is missing/invalid or ForceCodexCLI explicitly requests one.
type codexClientProfileKind int

const (
	codexClientProfileUbuntuCLI codexClientProfileKind = iota
	codexClientProfileWindowsDesktop
	codexClientProfileWindowsCLI
)

const (
	codexWindowsCLIUserAgentSuffix = " (Windows 10.0.26200; x86_64) WindowsTerminal"
	// Observed Codex Desktop 0.169.0-alpha.11.1 on Windows. The desktop app has
	// a different release train and a `(Codex Desktop; build)` UA trailer, so it
	// must not be rebuilt from the CLI version.
	codexWindowsDesktopUserAgent = "Codex Desktop/0.169.0-alpha.11.1 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.1111.21308)"
)

const (
	codexWindowsDesktopOriginator = "Codex Desktop"
	codexWindowsDesktopVersion    = "0.169.0-alpha.11.1"
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
	if profile, ok := codexClientProfileFromUserAgent(userAgent); ok {
		return profile
	}
	profile := codexClientProfileForKind(detectCodexClientProfileKind(userAgent, originator, metadataRaw))
	// TUI and exec register their own clientInfo.name during initialization.
	// Recover that identity when a proxy omitted the complete UA; unknown
	// clients still use the library's codex_cli_rs fallback.
	if profile.kind != codexClientProfileWindowsDesktop {
		if uaOriginator, _, ok := openai.PairCodexClientIdentity(userAgent); ok {
			originator = uaOriginator
		}
		for _, name := range []string{openai.CodexTUIOriginator, "codex_exec"} {
			if strings.EqualFold(originator, name) {
				suffix := codexCLIUserAgentSuffix
				if profile.kind == codexClientProfileWindowsCLI {
					suffix = codexWindowsCLIUserAgentSuffix
				}
				profile.originator = name
				profile.userAgent = name + "/" + profile.version + suffix + " (" + name + "; " + profile.version + ")"
				break
			}
		}
	}
	return profile
}

// codexClientProfileFromUserAgent only preserves complete, header-safe official
// UAs. Bare identity/version tokens use the configured fallback fingerprint.
// Desktop's trailer is an app build, independent of the engine version, so it
// must never be passed through SetCodexUserAgentVersion here.
func codexClientProfileFromUserAgent(userAgent string) (codexClientProfile, bool) {
	originator, ua, ok := openai.PairCodexClientIdentity(userAgent)
	if !ok {
		return codexClientProfile{}, false
	}
	version := NormalizeCodexClientVersion(openai.CodexUserAgentVersion(ua))
	if version == "" {
		return codexClientProfile{}, false
	}
	fingerprint := strings.TrimPrefix(ua, originator+"/"+version+" ")
	closeOS := strings.IndexByte(fingerprint, ')')
	if !strings.HasPrefix(fingerprint, "(") || closeOS < 0 ||
		!strings.Contains(fingerprint[:closeOS], ";") || strings.TrimSpace(fingerprint[closeOS+1:]) == "" {
		return codexClientProfile{}, false
	}
	kind := codexClientProfileUbuntuCLI
	if strings.HasPrefix(originator, "Codex ") || originator == "codex_chatgpt_desktop" {
		kind = codexClientProfileWindowsDesktop
	} else if strings.Contains(strings.ToLower(fingerprint[:closeOS]), "windows") {
		kind = codexClientProfileWindowsCLI
	}
	return codexClientProfile{kind: kind, originator: originator, userAgent: ua, version: version}, true
}

func detectCodexClientProfileKind(userAgent, originator, metadataRaw string) codexClientProfileKind {
	if profile, ok := codexClientProfileFromUserAgent(userAgent); ok {
		return profile.kind
	}
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	origin := strings.ToLower(strings.TrimSpace(originator))
	metadata := parseCodexClientProfileMetadata(metadataRaw)

	// Metadata identifies the fallback client when no complete official UA is
	// available. Workspace paths describe the execution host, which can differ
	// from the UI's OS, so they must not override a valid client UA.
	if strings.Contains(strings.ToLower(metadata.ClientType), "desktop") {
		return codexClientProfileWindowsDesktop
	}
	desktop := strings.HasPrefix(ua, "codex desktop/") ||
		strings.Contains(ua, "(codex desktop;") ||
		origin == "codex desktop" ||
		origin == "codex_chatgpt_desktop"
	if desktop {
		return codexClientProfileWindowsDesktop
	}
	if codexMetadataHasWindowsWorkspace(metadata) {
		return codexClientProfileWindowsCLI
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
// identity step. Explicit account UAs take priority; otherwise it uses the
// preserved client identity or the selected fallback. Native inference never
// adds a legacy Version header, including for account-level overrides.
func enforceCodexIdentityHeadersWithProfile(h http.Header, overrideUA string, profile codexClientProfile) {
	if h == nil || h.Get("originator") == "" {
		return
	}
	if !codexIdentityEnforcement.Load() {
		pairCodexIdentityHeaders(h)
		h.Del("version")
		return
	}
	if override, ok := codexClientProfileFromUserAgent(overrideUA); ok {
		// Account CLI fingerprints retain their existing version-sync policy.
		// Desktop has a separate engine/build release train; preserve both.
		if override.kind != codexClientProfileWindowsDesktop {
			override.version = CodexCanonicalClientVersion()
			override.userAgent = openai.SetCodexUserAgentVersion(override.userAgent, override.version)
		}
		profile = override
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
