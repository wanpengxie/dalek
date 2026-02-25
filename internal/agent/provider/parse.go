package provider

import (
	"encoding/json"
	"strings"
)

func parseOutputJSON(raw string) ParsedOutput {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParsedOutput{}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ParsedOutput{Text: raw}
	}
	text := strings.TrimSpace(collectText(obj))
	if text == "" {
		text = raw
	}
	return ParsedOutput{Text: text}
}

func parseOutputJSONL(raw string) ParsedOutput {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ParsedOutput{}
	}
	lines := strings.Split(raw, "\n")
	events := make([]any, 0, len(lines))
	texts := make([]string, 0, len(lines))
	parsedAny := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		parsedAny = true
		events = append(events, obj)
		if text := strings.TrimSpace(extractJSONLText(obj)); text != "" {
			texts = append(texts, text)
		}
	}
	if !parsedAny {
		return parseOutputJSON(raw)
	}
	return ParsedOutput{
		Text:   strings.TrimSpace(strings.Join(texts, "\n")),
		Events: events,
	}
}

func extractJSONLText(obj map[string]any) string {
	if item, ok := obj["item"].(map[string]any); ok {
		if s := strings.TrimSpace(collectText(item)); s != "" {
			return s
		}
	}
	if s := strings.TrimSpace(collectText(obj["text"])); s != "" {
		return s
	}
	if s := strings.TrimSpace(collectText(obj["content"])); s != "" {
		return s
	}
	if s := strings.TrimSpace(collectText(obj["result"])); s != "" {
		return s
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
			if s := strings.TrimSpace(collectText(it)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"message", "content", "result", "text"} {
			if s := strings.TrimSpace(collectText(x[key])); s != "" {
				return s
			}
		}
		return ""
	default:
		return ""
	}
}
