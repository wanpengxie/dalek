package channel

import (
	"encoding/json"
	"fmt"
	"strings"

	"dalek/internal/contracts"
)

func parseTurnResponseFromAgent(resp pmAgentTurnResponse) (contracts.TurnResponse, bool) {
	candidates := []string{
		resp.Text,
		resp.Stdout,
	}
	for _, candidate := range candidates {
		if tr, ok := parseTurnResponseCandidate(candidate); ok {
			return tr, true
		}
	}
	return contracts.TurnResponse{}, false
}

func parseTurnResponseCandidate(raw string) (contracts.TurnResponse, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return contracts.TurnResponse{}, false
	}
	if tr, ok := decodeTurnResponseJSON(raw); ok {
		return tr, true
	}

	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" {
			continue
		}
		if tr, ok := decodeTurnResponseJSON(line); ok {
			return tr, true
		}
		if idx := strings.Index(line, "{"); idx >= 0 {
			if tr, ok := decodeTurnResponseJSON(line[idx:]); ok {
				return tr, true
			}
		}
	}

	for _, block := range extractMarkdownCodeBlocks(raw) {
		if tr, ok := decodeTurnResponseJSON(block); ok {
			return tr, true
		}
	}
	return contracts.TurnResponse{}, false
}

func decodeTurnResponseJSON(raw string) (contracts.TurnResponse, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return contracts.TurnResponse{}, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return contracts.TurnResponse{}, false
	}
	if !looksLikeTurnResponseMap(m) {
		return contracts.TurnResponse{}, false
	}

	buf, err := json.Marshal(m)
	if err != nil {
		return contracts.TurnResponse{}, false
	}
	var out contracts.TurnResponse
	if err := json.Unmarshal(buf, &out); err != nil {
		return contracts.TurnResponse{}, false
	}
	out.Normalize()
	if err := out.Validate(); err != nil {
		return contracts.TurnResponse{}, false
	}
	return out, true
}

func looksLikeTurnResponseMap(m map[string]any) bool {
	if len(m) == 0 {
		return false
	}
	if schema := fmt.Sprint(m["schema"]); schema == contracts.TurnResponseSchemaV1 {
		return true
	}
	if _, ok := m["reply_text"]; ok {
		return true
	}
	if _, ok := m["actions"]; ok {
		return true
	}
	if _, ok := m["requires_confirmation"]; ok {
		return true
	}
	return false
}

func extractMarkdownCodeBlocks(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 2)
	searchFrom := 0
	for {
		start := strings.Index(raw[searchFrom:], "```")
		if start < 0 {
			break
		}
		start += searchFrom
		end := strings.Index(raw[start+3:], "```")
		if end < 0 {
			break
		}
		end += start + 3
		block := raw[start+3 : end]
		if nl := strings.Index(block, "\n"); nl >= 0 {
			firstLine := strings.ToLower(block[:nl])
			if firstLine == "json" || firstLine == "javascript" || firstLine == "js" {
				block = block[nl+1:]
			}
		}
		if block != "" {
			out = append(out, block)
		}
		searchFrom = end + 3
	}
	return out
}
