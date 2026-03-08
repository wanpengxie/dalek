package pm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dalek/internal/contracts"
)

const defaultPMPlanJSONRelativePath = ".dalek/pm/plan.json"

type SurfaceConflictStrategy string

const (
	SurfaceConflictNone             SurfaceConflictStrategy = "none"
	SurfaceConflictSerial           SurfaceConflictStrategy = "serial"
	SurfaceConflictIntegration      SurfaceConflictStrategy = "integration"
	SurfaceConflictParallelWithRisk SurfaceConflictStrategy = "parallel_with_risk"
)

type SurfaceConflict struct {
	TicketID           uint                    `json:"ticket_id"`
	OtherTicketID      uint                    `json:"other_ticket_id,omitempty"`
	TicketNodeIDs      []string                `json:"ticket_node_ids,omitempty"`
	OtherTicketNodeIDs []string                `json:"other_ticket_node_ids,omitempty"`
	OverlapSurfaces    []string                `json:"overlap_surfaces,omitempty"`
	OverlapCount       int                     `json:"overlap_count"`
	Strategy           SurfaceConflictStrategy `json:"strategy"`
	Reason             string                  `json:"reason,omitempty"`
}

type surfaceTicketInfo struct {
	TicketID  uint
	NodeIDs   []string
	Surfaces  []string
	IsActive  bool
	HasNode   bool
	NodeTypes []contracts.FeatureNodeType
}

type surfaceConflictIndex struct {
	Tickets              map[uint]surfaceTicketInfo
	ActiveTicketIDs      []uint
	IntegrationNodeCount int64
}

// DetectSurfaceConflicts 检测给定 ticket 集合的 touch_surfaces 冲突。
func DetectSurfaceConflicts(graph contracts.FeatureGraph, ticketIDs []uint) []SurfaceConflict {
	index := buildSurfaceConflictIndex(graph)
	return detectSurfaceConflictsFromIndex(index, ticketIDs)
}

func buildSurfaceConflictIndex(graph contracts.FeatureGraph) surfaceConflictIndex {
	type mutableInfo struct {
		nodeIDs   map[string]struct{}
		surfaces  map[string]struct{}
		active    bool
		nodeTypes map[contracts.FeatureNodeType]struct{}
	}
	mutable := map[uint]*mutableInfo{}
	integrationNodeCount := int64(0)

	for _, node := range graph.Nodes {
		if node.Type == contracts.FeatureNodeIntegration {
			integrationNodeCount++
		}
		if node.Type != contracts.FeatureNodeTicket && node.Type != contracts.FeatureNodeIntegration {
			continue
		}
		ticketID, ok := parseFeatureGraphTicketID(node.TicketID)
		if !ok || ticketID == 0 {
			continue
		}
		info, exists := mutable[ticketID]
		if !exists {
			info = &mutableInfo{
				nodeIDs:   map[string]struct{}{},
				surfaces:  map[string]struct{}{},
				nodeTypes: map[contracts.FeatureNodeType]struct{}{},
			}
			mutable[ticketID] = info
		}
		nodeID := strings.TrimSpace(node.ID)
		if nodeID != "" {
			info.nodeIDs[nodeID] = struct{}{}
		}
		for _, raw := range node.TouchSurfaces {
			norm := normalizeTouchSurface(raw)
			if norm == "" {
				continue
			}
			info.surfaces[norm] = struct{}{}
		}
		if isFeatureNodeActive(node.Status) {
			info.active = true
		}
		info.nodeTypes[node.Type] = struct{}{}
	}

	out := surfaceConflictIndex{
		Tickets:              make(map[uint]surfaceTicketInfo, len(mutable)),
		ActiveTicketIDs:      make([]uint, 0, len(mutable)),
		IntegrationNodeCount: integrationNodeCount,
	}
	for ticketID, info := range mutable {
		item := surfaceTicketInfo{
			TicketID:  ticketID,
			NodeIDs:   mapKeys(info.nodeIDs),
			Surfaces:  mapKeys(info.surfaces),
			IsActive:  info.active,
			HasNode:   len(info.nodeIDs) > 0,
			NodeTypes: make([]contracts.FeatureNodeType, 0, len(info.nodeTypes)),
		}
		for t := range info.nodeTypes {
			item.NodeTypes = append(item.NodeTypes, t)
		}
		sort.Strings(item.NodeIDs)
		sort.Strings(item.Surfaces)
		sort.Slice(item.NodeTypes, func(i, j int) bool {
			return strings.TrimSpace(string(item.NodeTypes[i])) < strings.TrimSpace(string(item.NodeTypes[j]))
		})
		out.Tickets[ticketID] = item
		if item.IsActive {
			out.ActiveTicketIDs = append(out.ActiveTicketIDs, ticketID)
		}
	}
	sort.Slice(out.ActiveTicketIDs, func(i, j int) bool { return out.ActiveTicketIDs[i] < out.ActiveTicketIDs[j] })
	return out
}

func detectSurfaceConflictsFromIndex(index surfaceConflictIndex, ticketIDs []uint) []SurfaceConflict {
	ids := uniqueSortedTicketIDs(ticketIDs)
	filtered := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, ok := index.Tickets[id]; ok {
			filtered = append(filtered, id)
		}
	}
	out := make([]SurfaceConflict, 0)
	for i := 0; i < len(filtered); i++ {
		left := index.Tickets[filtered[i]]
		for j := i + 1; j < len(filtered); j++ {
			right := index.Tickets[filtered[j]]
			conflict, ok := buildOverlapConflict(left, right)
			if !ok {
				continue
			}
			out = append(out, conflict)
		}
	}
	sortSurfaceConflicts(out)
	return out
}

func evaluateSurfaceConflictStrategyForTicket(ticketID uint, activeTicketIDs map[uint]bool, index surfaceConflictIndex) (SurfaceConflictStrategy, []SurfaceConflict) {
	if ticketID == 0 {
		return SurfaceConflictNone, nil
	}
	candidate, ok := index.Tickets[ticketID]
	if !ok {
		return SurfaceConflictParallelWithRisk, []SurfaceConflict{{
			TicketID: ticketID,
			Strategy: SurfaceConflictParallelWithRisk,
			Reason:   "ticket missing from feature graph",
		}}
	}

	conflicts := make([]SurfaceConflict, 0)
	decision := SurfaceConflictNone
	if len(candidate.Surfaces) == 0 {
		conflicts = append(conflicts, SurfaceConflict{
			TicketID:      ticketID,
			TicketNodeIDs: append([]string{}, candidate.NodeIDs...),
			Strategy:      SurfaceConflictParallelWithRisk,
			Reason:        "ticket has empty touch_surfaces",
		})
		decision = strongerSurfaceConflictStrategy(decision, SurfaceConflictParallelWithRisk)
	}

	activeIDs := make([]uint, 0, len(activeTicketIDs))
	for id := range activeTicketIDs {
		if id == 0 || id == ticketID {
			continue
		}
		activeIDs = append(activeIDs, id)
	}
	sort.Slice(activeIDs, func(i, j int) bool { return activeIDs[i] < activeIDs[j] })

	for _, otherID := range activeIDs {
		other, ok := index.Tickets[otherID]
		if !ok {
			continue
		}
		if len(other.Surfaces) == 0 {
			conflicts = append(conflicts, SurfaceConflict{
				TicketID:           ticketID,
				OtherTicketID:      otherID,
				TicketNodeIDs:      append([]string{}, candidate.NodeIDs...),
				OtherTicketNodeIDs: append([]string{}, other.NodeIDs...),
				Strategy:           SurfaceConflictParallelWithRisk,
				Reason:             "peer ticket has empty touch_surfaces",
			})
			decision = strongerSurfaceConflictStrategy(decision, SurfaceConflictParallelWithRisk)
			continue
		}
		conflict, hasOverlap := buildOverlapConflict(candidate, other)
		if !hasOverlap {
			continue
		}
		conflicts = append(conflicts, conflict)
		decision = strongerSurfaceConflictStrategy(decision, conflict.Strategy)
	}

	sortSurfaceConflicts(conflicts)
	return decision, conflicts
}

func buildOverlapConflict(left, right surfaceTicketInfo) (SurfaceConflict, bool) {
	overlap := intersectSortedStrings(left.Surfaces, right.Surfaces)
	if len(overlap) == 0 {
		return SurfaceConflict{}, false
	}
	strategy := surfaceStrategyForOverlap(len(overlap))
	return SurfaceConflict{
		TicketID:           left.TicketID,
		OtherTicketID:      right.TicketID,
		TicketNodeIDs:      append([]string{}, left.NodeIDs...),
		OtherTicketNodeIDs: append([]string{}, right.NodeIDs...),
		OverlapSurfaces:    overlap,
		OverlapCount:       len(overlap),
		Strategy:           strategy,
		Reason:             fmt.Sprintf("overlap surfaces=%d", len(overlap)),
	}, true
}

func isFeatureNodeActive(status contracts.FeatureNodeStatus) bool {
	switch status {
	case contracts.FeatureNodePending, contracts.FeatureNodeInProgress:
		return true
	default:
		return false
	}
}

func surfaceStrategyForOverlap(overlapCount int) SurfaceConflictStrategy {
	if overlapCount >= 3 {
		return SurfaceConflictSerial
	}
	if overlapCount >= 1 {
		return SurfaceConflictIntegration
	}
	return SurfaceConflictNone
}

func strongerSurfaceConflictStrategy(a, b SurfaceConflictStrategy) SurfaceConflictStrategy {
	if surfaceConflictStrategyRank(b) > surfaceConflictStrategyRank(a) {
		return b
	}
	return a
}

func surfaceConflictStrategyRank(s SurfaceConflictStrategy) int {
	switch s {
	case SurfaceConflictSerial:
		return 3
	case SurfaceConflictIntegration:
		return 2
	case SurfaceConflictParallelWithRisk:
		return 1
	default:
		return 0
	}
}

func sortSurfaceConflicts(conflicts []SurfaceConflict) {
	sort.Slice(conflicts, func(i, j int) bool {
		left := conflicts[i]
		right := conflicts[j]
		if surfaceConflictStrategyRank(left.Strategy) != surfaceConflictStrategyRank(right.Strategy) {
			return surfaceConflictStrategyRank(left.Strategy) > surfaceConflictStrategyRank(right.Strategy)
		}
		if left.TicketID != right.TicketID {
			return left.TicketID < right.TicketID
		}
		if left.OtherTicketID != right.OtherTicketID {
			return left.OtherTicketID < right.OtherTicketID
		}
		if left.OverlapCount != right.OverlapCount {
			return left.OverlapCount > right.OverlapCount
		}
		return strings.TrimSpace(left.Reason) < strings.TrimSpace(right.Reason)
	})
}

func uniqueSortedTicketIDs(src []uint) []uint {
	set := map[uint]struct{}{}
	for _, id := range src {
		if id == 0 {
			continue
		}
		set[id] = struct{}{}
	}
	out := make([]uint, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func normalizeTouchSurface(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(raw))
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "." {
		return ""
	}
	cleaned = strings.TrimPrefix(cleaned, "./")
	return strings.ToLower(cleaned)
}

func parseFeatureGraphTicketID(raw string) (uint, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	for _, candidate := range []string{
		raw,
		strings.TrimPrefix(strings.ToLower(raw), "t"),
		strings.TrimPrefix(strings.ToLower(raw), "ticket-"),
		strings.TrimPrefix(strings.ToLower(raw), "#"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if id, err := strconv.ParseUint(candidate, 10, 64); err == nil && id > 0 {
			return uint(id), true
		}
	}

	// fallback: allow forms like "t-12" / "ticket_34" by extracting trailing digits
	end := len(raw) - 1
	for end >= 0 && (raw[end] < '0' || raw[end] > '9') {
		end--
	}
	if end < 0 {
		return 0, false
	}
	start := end
	for start >= 0 && raw[start] >= '0' && raw[start] <= '9' {
		start--
	}
	digits := raw[start+1 : end+1]
	if digits == "" {
		return 0, false
	}
	id, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

func intersectSortedStrings(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	out := make([]string, 0)
	i := 0
	j := 0
	for i < len(left) && j < len(right) {
		switch {
		case left[i] == right[j]:
			out = append(out, left[i])
			i++
			j++
		case left[i] < right[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func mapKeys[T ~string](m map[T]struct{}) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		k := strings.TrimSpace(string(key))
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	return out
}

func uniqueSurfaceConflicts(src []SurfaceConflict) []SurfaceConflict {
	if len(src) == 0 {
		return nil
	}
	out := make([]SurfaceConflict, 0, len(src))
	seen := map[string]struct{}{}
	for _, item := range src {
		key := fmt.Sprintf("%d|%d|%s|%d|%s|%s",
			item.TicketID,
			item.OtherTicketID,
			item.Strategy,
			item.OverlapCount,
			strings.Join(item.OverlapSurfaces, ","),
			strings.TrimSpace(item.Reason),
		)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sortSurfaceConflicts(out)
	return out
}

func renderSurfaceConflictSummary(conflicts []SurfaceConflict) string {
	if len(conflicts) == 0 {
		return "touch_surfaces 冲突策略触发，建议 PM 评估是否串行或创建 integration 节点。"
	}
	lines := make([]string, 0, len(conflicts)+1)
	lines = append(lines, "touch_surfaces 冲突策略触发：")
	for _, item := range conflicts {
		target := fmt.Sprintf("t%d", item.OtherTicketID)
		if item.OtherTicketID == 0 {
			target = "-"
		}
		overlap := strings.Join(item.OverlapSurfaces, ", ")
		if overlap == "" {
			overlap = "-"
		}
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			reason = "surface conflict"
		}
		lines = append(lines, fmt.Sprintf("- t%d vs %s strategy=%s overlap=%s reason=%s", item.TicketID, target, item.Strategy, overlap, reason))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) loadSurfaceConflictIndex() (surfaceConflictIndex, bool, error) {
	if s == nil || s.p == nil {
		return surfaceConflictIndex{}, false, fmt.Errorf("pm service 缺少 project 上下文")
	}
	repoRoot := strings.TrimSpace(s.p.RepoRoot)
	if repoRoot == "" {
		return surfaceConflictIndex{}, false, nil
	}
	planJSONPath := filepath.Join(repoRoot, filepath.FromSlash(defaultPMPlanJSONRelativePath))
	raw, err := os.ReadFile(planJSONPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return surfaceConflictIndex{}, false, nil
		}
		return surfaceConflictIndex{}, false, err
	}
	var graph contracts.FeatureGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return surfaceConflictIndex{}, false, err
	}
	return buildSurfaceConflictIndex(graph), true, nil
}

func (s *Service) tryLoadSurfaceConflictIndex() (surfaceConflictIndex, bool) {
	index, found, err := s.loadSurfaceConflictIndex()
	if err != nil {
		s.slog().Warn("load surface conflict index failed",
			"error", err,
		)
		return surfaceConflictIndex{}, false
	}
	return index, found
}
