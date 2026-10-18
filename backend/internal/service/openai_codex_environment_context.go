package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const codexEnvironmentContextTimezone = "America/Los_Angeles"

var (
	codexEnvironmentContextBlockRE       = regexp.MustCompile(`(?s)<environment_context\b[^>]*>.*?</environment_context\s*>`)
	codexEnvironmentContextSelfClosingRE = regexp.MustCompile(`(?s)<environment_context\b[^>]*/\s*>`)
	codexEnvironmentContextCurrentDateRE = regexp.MustCompile(`(?s)<current_date\b[^>]*(?:/>|>.*?</current_date\s*>)`)
	codexEnvironmentContextTimezoneRE    = regexp.MustCompile(`(?s)<timezone\b[^>]*(?:/>|>.*?</timezone\s*>)`)
)

// codexEnvironmentContextValues returns the date and IANA timezone that Codex
// should see. The date is derived from the current instant after converting it
// to the fixed Codex/OpenAI US Pacific timezone, so a server running near
// midnight in another timezone cannot leak its local calendar date upstream.
func codexEnvironmentContextValues(now time.Time) (string, string) {
	location, err := time.LoadLocation(codexEnvironmentContextTimezone)
	if err != nil {
		// Keep the wire timezone stable even when a minimal runtime image does not
		// ship zoneinfo. The normal release images provide the IANA database.
		return now.UTC().Format("2006-01-02"), codexEnvironmentContextTimezone
	}
	return now.In(location).Format("2006-01-02"), codexEnvironmentContextTimezone
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

	return rewritten, found, changed
}

func rewriteCodexEnvironmentContextBody(body []byte) ([]byte, bool, error) {
	return rewriteCodexEnvironmentContextBodyAt(body, time.Now())
}

func rewriteCodexEnvironmentContextBodyAt(body []byte, now time.Time) ([]byte, bool, error) {
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

	currentDate, timezone := codexEnvironmentContextValues(now)
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

	rebuilt, err := marshalOpenAIUpstreamJSON(reqBody)
	if err != nil {
		return body, false, fmt.Errorf("encode Codex environment context: %w", err)
	}
	return rebuilt, true, nil
}
