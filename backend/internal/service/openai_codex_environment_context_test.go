package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexEnvironmentContextValuesConvertServerInstantToUSPacificDate(t *testing.T) {
	now := time.Date(2026, 1, 2, 1, 30, 0, 0, time.UTC)

	currentDate, timezone := codexEnvironmentContextValues(now)

	require.Equal(t, "2026-01-01", currentDate)
	require.Equal(t, "America/Los_Angeles", timezone)
}

func TestRewriteCodexEnvironmentContextBodyReplacesDateAndTimezone(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"<environment_context>\n  <cwd>/workspace</cwd>\n  <current_date>1999-12-31</current_date>\n  <timezone>Asia/Shanghai</timezone>\n</environment_context>"}]}`)
	now := time.Date(2026, 1, 2, 1, 30, 0, 0, time.UTC)

	rewritten, changed, err := rewriteCodexEnvironmentContextBodyAt(body, now)

	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(rewritten), "<cwd>/workspace</cwd>")
	require.Contains(t, string(rewritten), "<current_date>2026-01-01</current_date>")
	require.Contains(t, string(rewritten), "<timezone>America/Los_Angeles</timezone>")
	require.NotContains(t, string(rewritten), "1999-12-31")
	require.NotContains(t, string(rewritten), "Asia/Shanghai")
}

func TestRewriteCodexEnvironmentContextBodyPreservesBodyWhenContextMissing(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hello"}]}`)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	rewritten, changed, err := rewriteCodexEnvironmentContextBodyAt(body, now)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(rewritten))
}

func TestRewriteCodexEnvironmentContextBodyIsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"input":[{"type":"message","role":"user","content":"<environment_context>\n  <current_date>2026-07-04</current_date>\n  <timezone>America/Los_Angeles</timezone>\n</environment_context>"}]}`)

	rewritten, changed, err := rewriteCodexEnvironmentContextBodyAt(body, now)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(rewritten))
}

func TestRewriteCodexEnvironmentContextBodyHandlesNestedContentAndSelfClosingFields(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","content":[{"type":"input_text","text":"<environment_context><current_date status=\"unavailable\" /><timezone>UTC</timezone></environment_context>"}]}]}`)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	rewritten, changed, err := rewriteCodexEnvironmentContextBodyAt(body, now)

	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(rewritten), "<current_date>2026-07-04</current_date>")
	require.Contains(t, string(rewritten), "<timezone>America/Los_Angeles</timezone>")
}

func TestRewriteCodexEnvironmentContextBodyRejectsInvalidJSON(t *testing.T) {
	_, _, err := rewriteCodexEnvironmentContextBodyAt([]byte(`{"input":`), time.Now())

	require.Error(t, err)
}
