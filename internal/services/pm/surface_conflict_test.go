package pm

import (
	"testing"

	"dalek/internal/contracts"
)

func TestDetectSurfaceConflicts_OverlapStrategy(t *testing.T) {
	graph := contracts.FeatureGraph{
		Nodes: []contracts.FeatureNode{
			{
				ID:            "ticket-1",
				Type:          contracts.FeatureNodeTicket,
				Status:        contracts.FeatureNodeInProgress,
				TicketID:      "1",
				TouchSurfaces: []string{"internal/app/a.go", "internal/app/b.go", "internal/app/c.go"},
			},
			{
				ID:            "ticket-2",
				Type:          contracts.FeatureNodeTicket,
				Status:        contracts.FeatureNodePending,
				TicketID:      "2",
				TouchSurfaces: []string{"internal/app/b.go", "internal/app/c.go"},
			},
			{
				ID:            "ticket-3",
				Type:          contracts.FeatureNodeTicket,
				Status:        contracts.FeatureNodePending,
				TicketID:      "3",
				TouchSurfaces: []string{"internal/app/a.go", "internal/app/b.go", "internal/app/c.go", "internal/app/d.go"},
			},
		},
	}

	conflicts := DetectSurfaceConflicts(graph, []uint{1, 2, 3})
	if len(conflicts) != 3 {
		t.Fatalf("expected 3 conflicts, got=%d conflicts=%+v", len(conflicts), conflicts)
	}

	c12, ok := findSurfaceConflict(conflicts, 1, 2)
	if !ok {
		t.Fatalf("expected conflict between t1 and t2")
	}
	if c12.Strategy != SurfaceConflictIntegration {
		t.Fatalf("expected t1/t2 strategy=%s, got=%s", SurfaceConflictIntegration, c12.Strategy)
	}
	if c12.OverlapCount != 2 {
		t.Fatalf("expected t1/t2 overlap=2, got=%d", c12.OverlapCount)
	}

	c13, ok := findSurfaceConflict(conflicts, 1, 3)
	if !ok {
		t.Fatalf("expected conflict between t1 and t3")
	}
	if c13.Strategy != SurfaceConflictSerial {
		t.Fatalf("expected t1/t3 strategy=%s, got=%s", SurfaceConflictSerial, c13.Strategy)
	}
	if c13.OverlapCount != 3 {
		t.Fatalf("expected t1/t3 overlap=3, got=%d", c13.OverlapCount)
	}
}

func TestEvaluateSurfaceConflictStrategyForTicket_SerialDecision(t *testing.T) {
	graph := contracts.FeatureGraph{
		Nodes: []contracts.FeatureNode{
			{
				ID:            "ticket-1",
				Type:          contracts.FeatureNodeTicket,
				Status:        contracts.FeatureNodeInProgress,
				TicketID:      "1",
				TouchSurfaces: []string{"a.go", "b.go", "c.go"},
			},
			{
				ID:            "ticket-2",
				Type:          contracts.FeatureNodeTicket,
				Status:        contracts.FeatureNodePending,
				TicketID:      "2",
				TouchSurfaces: []string{"a.go", "b.go", "c.go"},
			},
		},
	}
	index := buildSurfaceConflictIndex(graph)
	strategy, conflicts := evaluateSurfaceConflictStrategyForTicket(2, map[uint]bool{1: true}, index)
	if strategy != SurfaceConflictSerial {
		t.Fatalf("expected strategy=%s, got=%s conflicts=%+v", SurfaceConflictSerial, strategy, conflicts)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected exactly one conflict, got=%d", len(conflicts))
	}
	if conflicts[0].OverlapCount != 3 {
		t.Fatalf("expected overlap=3, got=%d", conflicts[0].OverlapCount)
	}
}

func TestEvaluateSurfaceConflictStrategyForTicket_ParallelRiskWhenTouchSurfaceMissing(t *testing.T) {
	graph := contracts.FeatureGraph{
		Nodes: []contracts.FeatureNode{
			{
				ID:            "ticket-1",
				Type:          contracts.FeatureNodeTicket,
				Status:        contracts.FeatureNodeInProgress,
				TicketID:      "1",
				TouchSurfaces: []string{"a.go"},
			},
			{
				ID:       "ticket-2",
				Type:     contracts.FeatureNodeTicket,
				Status:   contracts.FeatureNodePending,
				TicketID: "2",
			},
		},
	}
	index := buildSurfaceConflictIndex(graph)
	strategy, conflicts := evaluateSurfaceConflictStrategyForTicket(2, map[uint]bool{1: true}, index)
	if strategy != SurfaceConflictParallelWithRisk {
		t.Fatalf("expected strategy=%s, got=%s conflicts=%+v", SurfaceConflictParallelWithRisk, strategy, conflicts)
	}
	if len(conflicts) == 0 {
		t.Fatalf("expected risk conflicts when touch_surfaces missing")
	}
}

func TestLoadSurfaceConflictIndex_MissingPlanJSON(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	_, found, err := svc.loadSurfaceConflictIndex()
	if err != nil {
		t.Fatalf("loadSurfaceConflictIndex should not error when plan.json missing: %v", err)
	}
	if found {
		t.Fatalf("expected found=false when plan.json missing")
	}
}

func findSurfaceConflict(conflicts []SurfaceConflict, a, b uint) (SurfaceConflict, bool) {
	for _, item := range conflicts {
		if item.TicketID == a && item.OtherTicketID == b {
			return item, true
		}
		if item.TicketID == b && item.OtherTicketID == a {
			return item, true
		}
	}
	return SurfaceConflict{}, false
}
