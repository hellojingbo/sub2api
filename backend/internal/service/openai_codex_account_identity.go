package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexAccountIdentityNamespaceVersion = "v1"

const codexAccountIdentitySourceContextKey = "openai_codex_account_identity_source"

const codexNativeSessionIdentityContextKey = "openai_codex_native_session_identity"

// codexNativeSessionIdentity is the single snapshot used by native OAuth
// Responses HTTP/WS handshakes. The source values are kept only for matching a
// staged snapshot; the values sent upstream are opaque UUIDs.
type codexNativeSessionIdentity struct {
	accountID  int64
	namespace  string
	rawSession string
	rawThread  string
	rawRequest string
	SessionID  string
	ThreadID   string
	RequestID  string
}

// prepareCodexAccountIdentitySource resolves credential shadows once per selected
// attempt. The handler reuses gin.Context across failover attempts, so every entry
// point overwrites the staged source before projecting outbound identity.
func (s *OpenAIGatewayService) prepareCodexAccountIdentitySource(ctx context.Context, c *gin.Context, account *Account) (*Account, error) {
	source := account
	if account != nil && account.IsShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		source = resolved
	}
	if c != nil {
		c.Set(codexAccountIdentitySourceContextKey, source)
	}
	return source, nil
}

func codexAccountIdentitySource(c *gin.Context, fallback *Account) *Account {
	if c != nil {
		if staged, ok := c.Get(codexAccountIdentitySourceContextKey); ok {
			if source, ok := staged.(*Account); ok && source != nil {
				return source
			}
		}
	}
	return fallback
}

// codexAccountIdentityNamespace returns a stable, credential-scoped namespace.
// Multiple local rows that use the same ChatGPT account intentionally share the
// same namespace. Setup tokens use an irreversible bearer fingerprint because
// they have no refresh lifecycle or imported account metadata. Refreshable OAuth
// otherwise falls back only to a persistent fingerprint seed: local row IDs are
// deployment-relative and must never become upstream identity.
func codexAccountIdentityNamespace(account *Account) string {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return ""
	}
	if upstreamAccountID := strings.TrimSpace(account.GetChatGPTAccountID()); upstreamAccountID != "" {
		if upstreamUserID := strings.TrimSpace(account.GetCredential("chatgpt_user_id")); upstreamUserID != "" {
			return "chatgpt:" + upstreamAccountID + ":user:" + upstreamUserID
		}
		return "chatgpt:" + upstreamAccountID
	}
	if seed, ok := codexFingerprintSeed(account.Extra); ok {
		return "seed:" + seed
	}
	if account.Type == AccountTypeSetupToken {
		if token := strings.TrimSpace(account.GetOpenAIAccessToken()); token != "" {
			sum := sha256.Sum256([]byte("openai-setup-token:" + token))
			return fmt.Sprintf("setup-token:%x", sum[:16])
		}
	}
	if token := strings.TrimSpace(account.GetOpenAIAccessToken()); token != "" {
		sum := sha256.Sum256([]byte("openai-oauth-token:" + token))
		return fmt.Sprintf("oauth-token:%x", sum[:16])
	}
	return ""
}

// isolateOpenAIUpstreamSessionID preserves the existing API-key isolation while
// adding the selected OAuth credential namespace. A scheduler failover therefore
// cannot send the same session/conversation identity through two upstream accounts.
func isolateOpenAIUpstreamSessionID(apiKeyID int64, account *Account, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if account != nil && account.IsOpenAIOAuthLike() {
		return scopeCodexAccountIdentityValue(account, apiKeyID, "legacy-session", raw)
	}
	namespace := codexAccountIdentityNamespace(account)
	if namespace == "" {
		return isolateOpenAISessionID(apiKeyID, raw)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("u%d:a%s:%s", apiKeyID, namespace, raw)))
	return fmt.Sprintf("%x", sum[:8])
}

func scopeCodexAccountIdentityValue(account *Account, apiKeyID int64, kind, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if account == nil || !account.IsOpenAIOAuthLike() {
		return raw
	}
	namespace := codexAccountIdentityNamespace(account)
	if namespace == "" {
		// A malformed or partially imported OAuth row still gets an account
		// scoped opaque value. The local row ID is hash input only and never
		// appears on the wire; normal imported accounts use the credential
		// namespace above.
		namespace = fmt.Sprintf("row:%d", account.ID)
	}
	return deriveStableUUIDv4(fmt.Sprintf(
		"codex-account-identity:%s:user:%d:account:%s:kind:%s:value:%s",
		codexAccountIdentityNamespaceVersion,
		apiKeyID,
		namespace,
		kind,
		raw,
	))
}

func shouldScopeCodexAccountIdentityValue(account *Account, raw string) bool {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return false
	}
	return codexAccountIdentityNamespace(account) != "" || strings.Contains(strings.ToLower(raw), "sub2api")
}

var codexAccountIdentityFields = []struct {
	name string
	kind string
}{
	{name: "installation_id", kind: "installation"},
	{name: "x-codex-installation-id", kind: "installation"},
	{name: "session_id", kind: "session"},
	{name: "session-id", kind: "session"},
	{name: "thread_id", kind: "thread"},
	{name: "thread-id", kind: "thread"},
	{name: "turn_id", kind: "turn"},
	{name: "turn-id", kind: "turn"},
	{name: "window_id", kind: "window"},
	{name: "x-codex-window-id", kind: "window"},
	{name: "x-client-request-id", kind: "request"},
}

func applyCodexAccountIdentityFields(values map[string]any, account *Account, apiKeyID int64) bool {
	if values == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return false
	}
	changed := false
	for _, field := range codexAccountIdentityFields {
		raw, ok := values[field.name].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		if !shouldScopeCodexAccountIdentityValue(account, raw) {
			continue
		}
		next := scopeCodexAccountIdentityValue(account, apiKeyID, field.kind, raw)
		if next != raw {
			values[field.name] = next
			changed = true
		}
	}
	return changed
}

func applyCodexAccountIdentityEmbeddedMetadata(values map[string]any, account *Account, apiKeyID int64) bool {
	raw, ok := values[openAIWSTurnMetadataHeader].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return false
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		return false
	}
	if !applyCodexAccountIdentityFields(metadata, account, apiKeyID) {
		return false
	}
	rebuilt, err := marshalCodexTurnMetadata(metadata)
	if err != nil {
		return false
	}
	values[openAIWSTurnMetadataHeader] = string(rebuilt)
	return true
}

func applyCodexAccountIdentityClientMetadataMap(requestBody map[string]any, account *Account, apiKeyID int64) bool {
	if requestBody == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return false
	}
	changed := false
	clientMetadata, _ := requestBody["client_metadata"].(map[string]any)
	originalBodySessionID := ""
	if clientMetadata != nil {
		originalBodySessionID, _ = clientMetadata["session_id"].(string)
		if applyCodexAccountIdentityFields(clientMetadata, account, apiKeyID) {
			changed = true
		}
		if applyCodexAccountIdentityEmbeddedMetadata(clientMetadata, account, apiKeyID) {
			changed = true
		}
	}
	if raw, ok := requestBody["prompt_cache_key"].(string); ok && strings.TrimSpace(raw) != "" {
		kind := "prompt-cache"
		if strings.TrimSpace(originalBodySessionID) != "" && raw == originalBodySessionID {
			kind = "session"
		}
		if !shouldScopeCodexAccountIdentityValue(account, raw) {
			return changed
		}
		next := scopeCodexAccountIdentityValue(account, apiKeyID, kind, raw)
		if next != raw {
			requestBody["prompt_cache_key"] = next
			changed = true
		}
	}
	return changed
}

// applyCodexAccountIdentityClientMetadataRaw scopes only the small identity
// subobjects with gjson/sjson. The passthrough hot path never unmarshals the
// potentially multi-megabyte request body.
func applyCodexAccountIdentityClientMetadataRaw(body []byte, account *Account, apiKeyID int64) ([]byte, bool, error) {
	if len(body) == 0 || account == nil || !account.IsOpenAIOAuthLike() {
		return body, false, nil
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return body, false, nil
	}

	next := body
	changed := false
	originalBodySessionID := ""
	if cm := gjson.GetBytes(body, "client_metadata"); cm.IsObject() {
		clientMetadata := map[string]any{}
		if err := json.Unmarshal([]byte(cm.Raw), &clientMetadata); err != nil {
			return body, false, fmt.Errorf("decode client_metadata for account identity: %w", err)
		}
		originalBodySessionID, _ = clientMetadata["session_id"].(string)
		metadataChanged := applyCodexAccountIdentityFields(clientMetadata, account, apiKeyID)
		if applyCodexAccountIdentityEmbeddedMetadata(clientMetadata, account, apiKeyID) {
			metadataChanged = true
		}
		if metadataChanged {
			raw, err := json.Marshal(clientMetadata)
			if err != nil {
				return body, false, fmt.Errorf("encode account-scoped client_metadata: %w", err)
			}
			var setErr error
			next, setErr = sjson.SetRawBytes(next, "client_metadata", raw)
			if setErr != nil {
				return body, false, fmt.Errorf("splice account-scoped client_metadata: %w", setErr)
			}
			changed = true
		}
	}
	if promptCacheKey := gjson.GetBytes(body, "prompt_cache_key"); promptCacheKey.Type == gjson.String && strings.TrimSpace(promptCacheKey.String()) != "" {
		raw := promptCacheKey.String()
		kind := "prompt-cache"
		if strings.TrimSpace(originalBodySessionID) != "" && raw == originalBodySessionID {
			kind = "session"
		}
		if !shouldScopeCodexAccountIdentityValue(account, raw) {
			return next, changed, nil
		}
		scoped := scopeCodexAccountIdentityValue(account, apiKeyID, kind, raw)
		if scoped != raw {
			rewritten, err := sjson.SetBytes(next, "prompt_cache_key", scoped)
			if err != nil {
				return body, false, fmt.Errorf("splice account-scoped prompt_cache_key: %w", err)
			}
			next = rewritten
			changed = true
		}
	}
	return next, changed, nil
}

func applyCodexAccountIdentityHeaders(headers http.Header, account *Account, apiKeyID int64) {
	if headers == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	for _, field := range codexAccountIdentityFields {
		// Underscore session/conversation headers are rebuilt separately from the
		// prompt cache key by each request builder.
		if field.name == "session_id" {
			continue
		}
		raw := strings.TrimSpace(headers.Get(field.name))
		if raw != "" && shouldScopeCodexAccountIdentityValue(account, raw) {
			headers.Set(field.name, scopeCodexAccountIdentityValue(account, apiKeyID, field.kind, raw))
		}
	}
	if raw := strings.TrimSpace(headers.Get(openAIWSTurnMetadataHeader)); raw != "" {
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &metadata); err == nil && metadata != nil && applyCodexAccountIdentityFields(metadata, account, apiKeyID) {
			if rebuilt, err := marshalCodexTurnMetadata(metadata); err == nil {
				headers.Set(openAIWSTurnMetadataHeader, string(rebuilt))
			}
		}
	}
}

func codexNativeSessionRawValue(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func codexNativeSessionIdentityFromBody(body []byte, promptCacheKey string) (string, string) {
	session := strings.TrimSpace(promptCacheKey)
	thread := ""
	if len(body) > 0 {
		if session == "" {
			if value := gjson.GetBytes(body, "client_metadata.session_id"); value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
				session = strings.TrimSpace(value.String())
			}
		}
		if value := gjson.GetBytes(body, "client_metadata.thread_id"); value.Type == gjson.String {
			thread = strings.TrimSpace(value.String())
		}
	}
	return session, thread
}

func isCodexNativeResponsesIngress(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return true
	}
	path := strings.ToLower(strings.TrimSpace(c.Request.URL.Path))
	return strings.HasSuffix(path, "/responses") || strings.Contains(path, "/responses/")
}

// stageCodexNativeSessionIdentity captures raw downstream identity before any
// body/account rewrite. It is intentionally OAuth-only and is overwritten on
// account failover so an identity from a previous credential cannot persist.
func stageCodexNativeSessionIdentity(c *gin.Context, account *Account, apiKeyID int64, promptCacheKey string, body []byte) *codexNativeSessionIdentity {
	if account == nil || !account.IsOpenAIOAuthLike() {
		if c != nil {
			c.Set(codexNativeSessionIdentityContextKey, (*codexNativeSessionIdentity)(nil))
		}
		return nil
	}
	source := codexAccountIdentitySource(c, account)
	namespace := codexAccountIdentityNamespace(source)
	identity := &codexNativeSessionIdentity{
		accountID: account.ID,
		namespace: namespace,
	}
	if source != nil {
		identity.accountID = source.ID
	}
	var headers http.Header
	if c != nil && c.Request != nil {
		headers = c.Request.Header
	}
	identity.rawSession = codexNativeSessionRawValue(headers, "session-id", "session_id", "conversation_id")
	identity.rawThread = codexNativeSessionRawValue(headers, "thread-id", "thread_id")
	identity.rawRequest = codexNativeSessionRawValue(headers, "x-client-request-id")
	bodySession, bodyThread := codexNativeSessionIdentityFromBody(body, promptCacheKey)
	if identity.rawSession == "" {
		identity.rawSession = bodySession
	}
	if identity.rawThread == "" {
		identity.rawThread = bodyThread
	}
	if identity.rawSession == "" {
		identity.rawSession = uuid.NewString()
	}
	if identity.rawThread == "" {
		identity.rawThread = identity.rawSession
	}
	if identity.rawRequest == "" {
		identity.rawRequest = uuid.NewString()
	}
	identity.SessionID = scopeCodexAccountIdentityValue(source, apiKeyID, "session", identity.rawSession)
	identity.ThreadID = scopeCodexAccountIdentityValue(source, apiKeyID, "thread", identity.rawThread)
	identity.RequestID = scopeCodexAccountIdentityValue(source, apiKeyID, "request", identity.rawRequest)
	if c != nil {
		c.Set(codexNativeSessionIdentityContextKey, identity)
	}
	return identity
}

func resolveCodexNativeSessionIdentity(c *gin.Context, account *Account, apiKeyID int64, promptCacheKey string, body []byte) *codexNativeSessionIdentity {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return nil
	}
	source := codexAccountIdentitySource(c, account)
	namespace := codexAccountIdentityNamespace(source)
	if c != nil {
		if value, ok := c.Get(codexNativeSessionIdentityContextKey); ok {
			if staged, ok := value.(*codexNativeSessionIdentity); ok && staged != nil && staged.accountID == source.ID && staged.namespace == namespace {
				return staged
			}
		}
	}
	return stageCodexNativeSessionIdentity(c, account, apiKeyID, promptCacheKey, body)
}

func clearCodexNativeSessionHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for _, name := range []string{"session-id", "thread-id", "x-client-request-id", "session_id", "conversation_id"} {
		headers.Del(name)
	}
}

func applyCodexNativeSessionHeaders(headers http.Header, identity *codexNativeSessionIdentity) {
	if headers == nil || identity == nil {
		return
	}
	clearCodexNativeSessionHeaders(headers)
	headers.Set("session-id", identity.SessionID)
	headers.Set("thread-id", identity.ThreadID)
	headers.Set("x-client-request-id", identity.RequestID)
}
