package contracts

import "testing"

func TestNextActionToSemanticPhase(t *testing.T) {
	tests := []struct {
		name       string
		nextAction string
		want       TaskSemanticPhase
	}{
		{name: "done", nextAction: "done", want: TaskPhaseDone},
		{name: "wait_user", nextAction: "wait_user", want: TaskPhaseBlocked},
		{name: "continue", nextAction: "continue", want: TaskPhaseImplementing},
		{name: "uppercase and spaces", nextAction: "  DONE  ", want: TaskPhaseDone},
		{name: "mixed case wait_user", nextAction: " Wait_User ", want: TaskPhaseBlocked},
		{name: "unknown fallback", nextAction: "noop", want: TaskPhaseImplementing},
		{name: "empty fallback", nextAction: "", want: TaskPhaseImplementing},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NextActionToSemanticPhase(tc.nextAction)
			if got != tc.want {
				t.Fatalf("NextActionToSemanticPhase(%q)=%q, want=%q", tc.nextAction, got, tc.want)
			}
		})
	}
}
