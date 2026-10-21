package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	codexEnvironmentContextDefaultTimezone  = "America/Los_Angeles"
	codexEnvironmentContextTimezoneExtraKey = "codex_environment_timezone"
)

var (
	codexEnvironmentContextBlockRE       = regexp.MustCompile(`(?s)<environment_context\b[^>]*>.*?</environment_context\s*>`)
	codexEnvironmentContextSelfClosingRE = regexp.MustCompile(`(?s)<environment_context\b[^>]*/\s*>`)
	codexEnvironmentContextCurrentDateRE = regexp.MustCompile(`(?s)<current_date\b[^>]*(?:/>|>.*?</current_date\s*>)`)
	codexEnvironmentContextTimezoneRE    = regexp.MustCompile(`(?s)<timezone\b[^>]*(?:/>|>.*?</timezone\s*>)`)
	// Codex App injects a separate developer message that tells the model to
	// prefer the client's local date/time over the execution host's values.
	// Leaving it untouched would keep exposing the real downstream timezone
	// after the environment context has been aligned with the proxy region.
	codexAppsClientTimeContextBlockRE       = regexp.MustCompile(`(?s)<codex_apps_client_time_context\b[^>]*>.*?</codex_apps_client_time_context\s*>`)
	codexAppsClientTimeContextSelfClosingRE = regexp.MustCompile(`(?s)<codex_apps_client_time_context\b[^>]*/\s*>`)
)

// normalizeCodexEnvironmentContextTimezone validates an IANA timezone and
// returns an empty string when it is missing or invalid. Empty/invalid values
// are resolved to the default at the call site so an account-level typo cannot
// make the environment context inconsistent.
func normalizeCodexEnvironmentContextTimezone(timezone string) string {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return ""
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return ""
	}
	return timezone
}

// codexEnvironmentContextValues returns the date and IANA timezone that Codex
// should see. The date is derived from the current instant in the effective
// timezone, so it matches the timezone sent in the same environment context.
func codexEnvironmentContextValues(now time.Time, timezone string) (string, string) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = codexEnvironmentContextDefaultTimezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		timezone = codexEnvironmentContextDefaultTimezone
		location, err = time.LoadLocation(timezone)
		if err != nil {
			// Keep the wire timezone stable even when a minimal runtime image does
			// not ship zoneinfo. The normal release images provide the IANA database.
			return now.UTC().Format("2006-01-02"), timezone
		}
	}
	return now.In(location).Format("2006-01-02"), timezone
}

func renderCodexEnvironmentContextBlock(block, currentDate, timezone string) string {
	openingEnd := strings.IndexByte(block, '>')
	if openingEnd < 0 {
		return block
	}

	lowerBlock := strings.ToLower(block)
	closingStart := strings.LastIndex(lowerBlock, "</environment_context")
	if closingStart < 0 {
		return block
	}
	closingEndRelative := strings.IndexByte(block[closingStart:], '>')
	if closingEndRelative < 0 {
		return block
	}
	closingEnd := closingStart + closingEndRelative + 1

	inner := block[openingEnd+1 : closingStart]
	inner = codexEnvironmentContextCurrentDateRE.ReplaceAllString(inner, "")
	inner = codexEnvironmentContextTimezoneRE.ReplaceAllString(inner, "")
	inner = strings.TrimRight(inner, " \t\r\n")
	if inner == "" {
		inner = "\n"
	} else {
		inner += "\n"
	}
	inner += "  <current_date>" + currentDate + "</current_date>\n"
	inner += "  <timezone>" + timezone + "</timezone>\n"

	return block[:openingEnd+1] + inner + block[closingStart:closingEnd]
}

// renderCodexAppsClientTimeContextBlock replaces the date/timezone values in
// place so the rest of the developer message (especially the instruction line)
// keeps its original wording and ordering.
func renderCodexAppsClientTimeContextBlock(block, currentDate, timezone string) string {
	openingEnd := strings.IndexByte(block, '>')
	if openingEnd < 0 {
		return block
	}

	lowerBlock := strings.ToLower(block)
	closingStart := strings.LastIndex(lowerBlock, "</codex_apps_client_time_context")
	if closingStart < 0 {
		return block
	}
	closingEndRelative := strings.IndexByte(block[closingStart:], '>')
	if closingEndRelative < 0 {
		return block
	}
	closingEnd := closingStart + closingEndRelative + 1

	inner := block[openingEnd+1 : closingStart]
	inner = codexEnvironmentContextCurrentDateRE.ReplaceAllString(inner, "<current_date>"+currentDate+"</current_date>")
	inner = codexEnvironmentContextTimezoneRE.ReplaceAllString(inner, "<timezone>"+timezone+"</timezone>")

	return block[:openingEnd+1] + inner + block[closingStart:closingEnd]
}

// rewriteCodexEnvironmentContextText aligns both time contexts that Codex can
// send: the execution-host environment context and the Codex App client time
// context. They must use the same effective date/timezone so an upstream
// request does not carry conflicting location hints.
func rewriteCodexEnvironmentContextText(text string, currentDate, timezone string) (string, bool, bool) {
	found := false
	changed := false

	rewritten := codexEnvironmentContextBlockRE.ReplaceAllStringFunc(text, func(block string) string {
		found = true
		next := renderCodexEnvironmentContextBlock(block, currentDate, timezone)
		if next != block {
			changed = true
		}
		return next
	})
	rewritten = codexEnvironmentContextSelfClosingRE.ReplaceAllStringFunc(rewritten, func(block string) string {
		found = true
		var replacement strings.Builder
		replacement.WriteString("<environment_context>\n  <current_date>")
		replacement.WriteString(currentDate)
		replacement.WriteString("</current_date>\n  <timezone>")
		replacement.WriteString(timezone)
		replacement.WriteString("</timezone>\n</environment_context>")
		next := replacement.String()
		if next != block {
			changed = true
		}
		return next
	})
	rewritten = codexAppsClientTimeContextBlockRE.ReplaceAllStringFunc(rewritten, func(block string) string {
		found = true
		next := renderCodexAppsClientTimeContextBlock(block, currentDate, timezone)
		if next != block {
			changed = true
		}
		return next
	})
	rewritten = codexAppsClientTimeContextSelfClosingRE.ReplaceAllStringFunc(rewritten, func(block string) string {
		found = true
		var replacement strings.Builder
		replacement.WriteString("<codex_apps_client_time_context><timezone>")
		replacement.WriteString(timezone)
		replacement.WriteString("</timezone>\n<current_date>")
		replacement.WriteString(currentDate)
		replacement.WriteString("</current_date></codex_apps_client_time_context>")
		next := replacement.String()
		if next != block {
			changed = true
		}
		return next
	})

	return rewritten, found, changed
}

func rewriteCodexEnvironmentContextBody(body []byte) ([]byte, bool, error) {
	return rewriteCodexEnvironmentContextBodyWithTimezone(body, codexEnvironmentContextDefaultTimezone)
}

func rewriteCodexEnvironmentContextBodyWithTimezone(body []byte, timezone string) ([]byte, bool, error) {
	return rewriteCodexEnvironmentContextBodyWithTimezoneAt(body, time.Now(), timezone)
}

func rewriteCodexEnvironmentContextBodyAt(body []byte, now time.Time) ([]byte, bool, error) {
	return rewriteCodexEnvironmentContextBodyWithTimezoneAt(body, now, codexEnvironmentContextDefaultTimezone)
}

func rewriteCodexEnvironmentContextBodyWithTimezoneAt(body []byte, now time.Time, timezone string) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}

	var reqBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &reqBody); err != nil {
		return body, false, fmt.Errorf("decode Codex environment context: %w", err)
	}
	if reqBody == nil {
		return body, false, fmt.Errorf("decode Codex environment context: expected a JSON object")
	}

	currentDate, timezone := codexEnvironmentContextValues(now, timezone)
	found := false
	changed := false
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if text, ok := child.(string); ok {
					rewritten, hasContext, textChanged := rewriteCodexEnvironmentContextText(text, currentDate, timezone)
					if hasContext {
						found = true
					}
					if textChanged {
						typed[key] = rewritten
						changed = true
					}
					continue
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(reqBody)

	if !found || !changed {
		return body, false, nil
	}

	rebuilt, err := marshalCodexResponsesJSON(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("encode Codex environment context: %w", err)
	}
	return rebuilt, true, nil
}
