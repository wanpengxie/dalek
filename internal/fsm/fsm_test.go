package fsm

import (
	"strings"
	"testing"
)

type testState string

const (
	stateQueued  testState = "queued"
	stateRunning testState = "running"
	stateDone    testState = "done"
	stateFailed  testState = "failed"
)

func sampleTable() TransitionTable[testState] {
	return TransitionTable[testState]{
		Name:          "sample",
		InitialStates: []testState{stateQueued},
		TerminalStates: []testState{
			stateDone,
		},
		Transitions: map[testState][]testState{
			stateQueued: {
				stateRunning,
			},
			stateRunning: {
				stateDone,
				stateFailed,
			},
			stateFailed: {
				stateRunning,
			},
			stateDone: {},
		},
	}
}

func TestCanTransition(t *testing.T) {
	table := sampleTable()

	if !table.CanTransition(stateQueued, stateRunning) {
		t.Fatalf("expected queued -> running to be allowed")
	}
	if !CanTransition(table, stateRunning, stateDone) {
		t.Fatalf("expected running -> done to be allowed via top-level function")
	}
	if table.CanTransition(stateQueued, stateDone) {
		t.Fatalf("expected queued -> done to be rejected")
	}
}

func TestMustTransition(t *testing.T) {
	table := sampleTable()
	table.MustTransition(stateQueued, stateRunning)

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected panic on invalid transition")
		}
		msg := recovered.(string)
		if !strings.Contains(msg, "invalid transition") {
			t.Fatalf("unexpected panic message: %q", msg)
		}
	}()
	table.MustTransition(stateQueued, stateDone)
}

func TestValidTargets(t *testing.T) {
	table := sampleTable()

	targets := table.ValidTargets(stateRunning)
	if len(targets) != 2 {
		t.Fatalf("unexpected target count: %d", len(targets))
	}
	if targets[0] != stateDone || targets[1] != stateFailed {
		t.Fatalf("unexpected targets: %v", targets)
	}

	targets[0] = "mutated"
	again := table.ValidTargets(stateRunning)
	if again[0] != stateDone {
		t.Fatalf("valid targets must return a copy, got=%v", again)
	}
}

func TestValidTransitionsAlias(t *testing.T) {
	table := sampleTable()

	a := table.ValidTransitions(stateQueued)
	b := ValidTransitions(table, stateQueued)
	c := ValidTargets(table, stateQueued)
	if len(a) != 1 || len(b) != 1 || len(c) != 1 {
		t.Fatalf("expected one valid target, got %v %v %v", a, b, c)
	}
	if a[0] != stateRunning || b[0] != stateRunning || c[0] != stateRunning {
		t.Fatalf("alias functions mismatch: %v %v %v", a, b, c)
	}
}

func TestIsTerminalAndKnownState(t *testing.T) {
	table := sampleTable()

	if !table.IsTerminal(stateDone) || !IsTerminal(table, stateDone) {
		t.Fatalf("expected done to be terminal")
	}
	if table.IsTerminal(stateRunning) {
		t.Fatalf("running must not be terminal")
	}
	if !table.IsKnownState(stateFailed) {
		t.Fatalf("failed should be known")
	}
	if table.IsKnownState(testState("missing")) {
		t.Fatalf("missing should be unknown")
	}
}

func TestAllStates(t *testing.T) {
	table := sampleTable()

	all := table.AllStates()
	if len(all) != 4 {
		t.Fatalf("unexpected all states len: %d", len(all))
	}
	found := map[testState]bool{}
	for _, state := range all {
		found[state] = true
	}
	for _, want := range []testState{stateQueued, stateRunning, stateDone, stateFailed} {
		if !found[want] {
			t.Fatalf("state missing from AllStates: %s", want)
		}
	}
}

func TestValidate(t *testing.T) {
	table := sampleTable()
	if err := table.Validate(); err != nil {
		t.Fatalf("expected valid table, got error: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		table   TransitionTable[testState]
		wantMsg string
	}{
		{
			name:    "empty table",
			table:   TransitionTable[testState]{Name: "empty"},
			wantMsg: "empty state table",
		},
		{
			name: "duplicate initial",
			table: TransitionTable[testState]{
				Name:          "dup-initial",
				InitialStates: []testState{stateQueued, stateQueued},
				Transitions:   map[testState][]testState{stateQueued: {stateRunning}},
			},
			wantMsg: "duplicate initial state",
		},
		{
			name: "duplicate terminal",
			table: TransitionTable[testState]{
				Name:           "dup-terminal",
				TerminalStates: []testState{stateDone, stateDone},
				Transitions:    map[testState][]testState{stateQueued: {stateRunning}},
			},
			wantMsg: "duplicate terminal state",
		},
		{
			name: "terminal has outgoing transitions",
			table: TransitionTable[testState]{
				Name:           "terminal-outgoing",
				TerminalStates: []testState{stateDone},
				Transitions:    map[testState][]testState{stateDone: {stateRunning}},
			},
			wantMsg: "terminal state has outgoing transitions",
		},
		{
			name: "duplicate transition",
			table: TransitionTable[testState]{
				Name:        "dup-transition",
				Transitions: map[testState][]testState{stateRunning: {stateDone, stateDone}},
			},
			wantMsg: "duplicate transition",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.table.Validate()
			if err == nil {
				t.Fatalf("expected validate error")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
