package agentcli

import (
	"encoding/json"
	"strings"
)

func parseOutput(raw string, mode OutputMode, backend Backend) (text string, sessionID string, events []Event) {
	trimmed := raw
	if trimmed == "" {
		return "", "", nil
	}

	switch mode {
	case OutputJSON:
		if t, sid, ok := parseJSON(trimmed, backend.SessionFields); ok {
			return t, sid, nil
		}
		if t, sid, ok := parseJSONFromMixedLines(trimmed, backend.SessionFields); ok {
			return t, sid, nil
		}
		return trimmed, "", nil
	case OutputJSONL:
		if t, sid, evts, ok := parseJSONL(trimmed, backend.SessionFields); ok {
			return t, sid, evts
		}
		if t, sid, ok := parseJSON(trimmed, backend.SessionFields); ok {
			return t, sid, nil
		}
		return trimmed, "", nil
	default:
		return trimmed, "", nil
	}
}

func parseJSONFromMixedLines(raw string, sessionFields []string) (text string, sessionID string, ok bool) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return "", "", false
	}

	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" {
			continue
		}
		candidates := []string{line}
		if idx := strings.Index(line, "{"); idx > 0 {
			candidates = append(candidates, line[idx:])
		}
		for _, cand := range candidates {
			cand = strings.TrimSpace(cand)
			if cand == "" {
				continue
			}
			if !strings.HasPrefix(cand, "{") {
				continue
			}
			if t, sid, ok := parseJSON(cand, sessionFields); ok {
				return t, sid, true
			}
		}
	}
	return "", "", false
}

func parseJSON(raw string, sessionFields []string) (text string, sessionID string, ok bool) {
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", "", false
	}
	obj, isObj := parsed.(map[string]any)
	if !isObj {
		return "", "", false
	}

	sessionID = pickSessionID(obj, sessionFields)
	text =
		collectText(obj["message"]) +
			collectText(obj["content"]) +
			collectText(obj["result"])

	if text == "" {
		text = collectText(obj)
	}
	return text, sessionID, true
}

func parseJSONL(raw string, sessionFields []string) (text string, sessionID string, events []Event, ok bool) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return "", "", nil, false
	}

	var texts []string
	var parsedAny bool

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var parsed any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			continue
		}
		obj, isObj := parsed.(map[string]any)
		if !isObj {
			continue
		}
		parsedAny = true

		if sessionID == "" {
			sessionID = pickSessionID(obj, sessionFields)
		}

		eventType := readString(obj["type"])
		eventText := extractJSONLEventText(obj)
		if eventType != "" || eventText != "" {
			events = append(events, Event{
				Type: eventType,
				Text: eventText,
			})
		}
		if eventText != "" {
			texts = append(texts, eventText)
		}
	}
	if !parsedAny {
		return "", "", nil, false
	}
	return strings.Join(texts, "\n"), sessionID, events, true
}

func extractJSONLEventText(obj map[string]any) string {
	if item, ok := obj["item"].(map[string]any); ok {
		itemText := collectText(item)
		if itemText != "" {
			return itemText
		}
	}
	text := collectText(obj["text"])
	if text != "" {
		return text
	}
	return collectText(obj["content"])
}

func pickSessionID(obj map[string]any, sessionFields []string) string {
	fields := sessionFields
	if len(fields) == 0 {
		fields = defaultSessionFields()
	}
	for _, field := range fields {
		v := readString(obj[field])
		if v != "" {
			return v
		}
	}
	return ""
}

func collectText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			if text := collectText(it); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if s := readString(x["text"]); s != "" {
			return s
		}
		if s := readString(x["content"]); s != "" {
			return s
		}
		if msg, ok := x["message"]; ok {
			if s := collectText(msg); s != "" {
				return s
			}
		}
		if content, ok := x["content"]; ok {
			if s := collectText(content); s != "" {
				return s
			}
		}
		if result, ok := x["result"]; ok {
			if s := collectText(result); s != "" {
				return s
			}
		}
		return ""
	default:
		return ""
	}
}

func readString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
