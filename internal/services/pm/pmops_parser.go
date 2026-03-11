package pm

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
)

const (
	plannerPMOpsMarkerOpen  = "<pmops>"
	plannerPMOpsMarkerClose = "</pmops>"
)

func parsePlannerPMOpsFromResult(res sdkrunner.Result, plannerRunID uint, requestID string) ([]contracts.PMOp, error) {
	candidates := []string{
		strings.TrimSpace(res.Text),
		strings.TrimSpace(res.Stdout),
		strings.TrimSpace(res.Stderr),
	}
	var lastErr error
	for _, raw := range candidates {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		ops, err := parsePlannerPMOps(raw, plannerRunID, requestID)
		if err != nil {
			lastErr = err
			continue
		}
		if len(ops) > 0 {
			return ops, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func parsePlannerPMOps(raw string, plannerRunID uint, requestID string) ([]contracts.PMOp, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if marked, ok := extractPlannerPMOpsBlock(raw); ok {
		ops, err := decodePlannerPMOpsJSON(marked)
		if err != nil {
			return nil, fmt.Errorf("解析 <pmops> 块失败: %w", err)
		}
		return normalizePlannerPMOps(ops, plannerRunID, requestID), nil
	}

	payloads := []string{raw}
	if block, ok := extractFirstJSONPayload(raw); ok {
		payloads = append(payloads, block)
	}
	var lastErr error
	for _, payload := range payloads {
		ops, err := decodePlannerPMOpsJSON(payload)
		if err != nil {
			lastErr = err
			continue
		}
		if len(ops) == 0 {
			continue
		}
		return normalizePlannerPMOps(ops, plannerRunID, requestID), nil
	}

	if !strings.Contains(raw, "\"kind\"") && !strings.Contains(raw, "'kind'") && !strings.Contains(raw, "pmops") {
		return nil, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("解析 PMOps JSON 失败: %w", lastErr)
	}
	return nil, nil
}

func decodePlannerPMOpsJSON(raw string) ([]contracts.PMOp, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var arr []contracts.PMOp
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr, nil
	}
	var obj struct {
		Ops        []contracts.PMOp `json:"ops"`
		PMOps      []contracts.PMOp `json:"pmops"`
		Operations []contracts.PMOp `json:"operations"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, err
	}
	switch {
	case len(obj.Ops) > 0:
		return obj.Ops, nil
	case len(obj.PMOps) > 0:
		return obj.PMOps, nil
	case len(obj.Operations) > 0:
		return obj.Operations, nil
	default:
		return nil, nil
	}
}

func extractPlannerPMOpsBlock(raw string) (string, bool) {
	lower := strings.ToLower(raw)
	start := strings.Index(lower, plannerPMOpsMarkerOpen)
	if start < 0 {
		return "", false
	}
	afterStart := start + len(plannerPMOpsMarkerOpen)
	end := strings.Index(lower[afterStart:], plannerPMOpsMarkerClose)
	if end < 0 {
		return "", false
	}
	end += afterStart
	block := strings.TrimSpace(raw[afterStart:end])
	if block == "" {
		return "", false
	}
	return block, true
}

func extractFirstJSONPayload(raw string) (string, bool) {
	type delimiter struct {
		open  byte
		close byte
	}
	candidates := []delimiter{
		{open: '[', close: ']'},
		{open: '{', close: '}'},
	}
	for _, d := range candidates {
		block, ok := extractBalancedBlock(raw, d.open, d.close)
		if ok {
			return block, true
		}
	}
	return "", false
}

func extractBalancedBlock(raw string, open, close byte) (string, bool) {
	start := strings.IndexByte(raw, open)
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == open {
			depth++
			continue
		}
		if c == close {
			depth--
			if depth == 0 {
				return strings.TrimSpace(raw[start : i+1]), true
			}
		}
	}
	return "", false
}

func normalizePlannerPMOps(in []contracts.PMOp, plannerRunID uint, requestID string) []contracts.PMOp {
	if len(in) == 0 {
		return nil
	}
	out := make([]contracts.PMOp, 0, len(in))
	opIDSeen := map[string]int{}
	for idx := range in {
		op := in[idx]
		op.Kind = normalizePlannerPMOpKind(op.Kind)
		if strings.TrimSpace(string(op.Kind)) == "" {
			continue
		}
		op.FeatureID = strings.TrimSpace(op.FeatureID)
		op.RequestID = strings.TrimSpace(op.RequestID)
		if op.RequestID == "" {
			op.RequestID = strings.TrimSpace(requestID)
		}
		op.Arguments = contracts.JSONMapFromAny(op.Arguments)
		op.Preconditions = contracts.JSONStringSliceFromAny(op.Preconditions)
		op.OpID = strings.TrimSpace(op.OpID)
		if op.OpID == "" {
			op.OpID = fmt.Sprintf("planner_run_%d_op_%d", plannerRunID, idx+1)
		}
		opIDSeen[op.OpID]++
		if opIDSeen[op.OpID] > 1 {
			op.OpID = fmt.Sprintf("%s_%d", op.OpID, opIDSeen[op.OpID])
		}
		op.IdempotencyKey = strings.TrimSpace(op.IdempotencyKey)
		if op.IdempotencyKey == "" {
			op.IdempotencyKey = buildPMOpIdempotencyKey(op, plannerRunID)
		}
		op.Critical = plannerPMOpIsCritical(op)
		out = append(out, op)
	}
	return out
}

func normalizePlannerPMOpKind(kind contracts.PMOpKind) contracts.PMOpKind {
	return contracts.PMOpKind(strings.TrimSpace(string(kind)))
}

func plannerPMOpIsCritical(op contracts.PMOp) bool {
	if op.Critical {
		return true
	}
	switch op.Kind {
	case contracts.PMOpCreateTicket, contracts.PMOpStartTicket, contracts.PMOpCreateIntegration:
		return true
	default:
		return false
	}
}

func buildPMOpIdempotencyKey(op contracts.PMOp, plannerRunID uint) string {
	payload := strings.TrimSpace(marshalJSON(map[string]any{
		"kind":          strings.TrimSpace(string(op.Kind)),
		"feature_id":    strings.TrimSpace(op.FeatureID),
		"request_id":    strings.TrimSpace(op.RequestID),
		"arguments":     contracts.JSONMapFromAny(op.Arguments),
		"preconditions": contracts.JSONStringSliceFromAny(op.Preconditions),
	}))
	sum := sha1.Sum([]byte(payload))
	hex8 := hex.EncodeToString(sum[:8])
	if strings.TrimSpace(string(op.Kind)) == "" {
		return fmt.Sprintf("planner_run_%d:%s", plannerRunID, hex8)
	}
	return fmt.Sprintf("%s:%s", strings.TrimSpace(string(op.Kind)), hex8)
}
