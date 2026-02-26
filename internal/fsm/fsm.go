package fsm

import (
	"fmt"
	"strings"
)

// TransitionTable 是一个纯函数可复用的状态转换表。
//
// 约定：
// - 只有显式声明在 Transitions[from] 中的目标状态才被视为合法转换。
// - 不声明 self-loop 时，from==to 默认不合法（幂等写入由上层自行处理）。
type TransitionTable[S comparable] struct {
	Name string

	InitialStates  []S
	TerminalStates []S
	Transitions    map[S][]S
}

func (t TransitionTable[S]) CanTransition(from, to S) bool {
	targets, ok := t.Transitions[from]
	if !ok {
		return false
	}
	for _, candidate := range targets {
		if candidate == to {
			return true
		}
	}
	return false
}

func (t TransitionTable[S]) MustTransition(from, to S) {
	if t.CanTransition(from, to) {
		return
	}
	panic(fmt.Sprintf("fsm %s: invalid transition %v -> %v", t.nameOrDefault(), from, to))
}

func (t TransitionTable[S]) ValidTargets(from S) []S {
	targets, ok := t.Transitions[from]
	if !ok || len(targets) == 0 {
		return nil
	}
	out := make([]S, len(targets))
	copy(out, targets)
	return out
}

// ValidTransitions 是 ValidTargets 的语义同义名。
func (t TransitionTable[S]) ValidTransitions(from S) []S {
	return t.ValidTargets(from)
}

func (t TransitionTable[S]) IsTerminal(state S) bool {
	for _, terminal := range t.TerminalStates {
		if terminal == state {
			return true
		}
	}
	return false
}

func (t TransitionTable[S]) IsKnownState(state S) bool {
	_, ok := t.stateSet()[state]
	return ok
}

func (t TransitionTable[S]) AllStates() []S {
	set := t.stateSet()
	if len(set) == 0 {
		return nil
	}
	out := make([]S, 0, len(set))
	for state := range set {
		out = append(out, state)
	}
	return out
}

func (t TransitionTable[S]) Validate() error {
	name := t.nameOrDefault()
	if len(t.AllStates()) == 0 {
		return fmt.Errorf("fsm %s: empty state table", name)
	}

	initialSeen := make(map[S]struct{}, len(t.InitialStates))
	for _, state := range t.InitialStates {
		if _, exists := initialSeen[state]; exists {
			return fmt.Errorf("fsm %s: duplicate initial state: %v", name, state)
		}
		initialSeen[state] = struct{}{}
	}

	terminalSeen := make(map[S]struct{}, len(t.TerminalStates))
	for _, state := range t.TerminalStates {
		if _, exists := terminalSeen[state]; exists {
			return fmt.Errorf("fsm %s: duplicate terminal state: %v", name, state)
		}
		terminalSeen[state] = struct{}{}
		if targets, ok := t.Transitions[state]; ok && len(targets) > 0 {
			return fmt.Errorf("fsm %s: terminal state has outgoing transitions: %v", name, state)
		}
	}

	for from, targets := range t.Transitions {
		seen := make(map[S]struct{}, len(targets))
		for _, to := range targets {
			if _, exists := seen[to]; exists {
				return fmt.Errorf("fsm %s: duplicate transition %v -> %v", name, from, to)
			}
			seen[to] = struct{}{}
		}
	}

	return nil
}

func (t TransitionTable[S]) stateSet() map[S]struct{} {
	out := make(map[S]struct{})
	for _, state := range t.InitialStates {
		out[state] = struct{}{}
	}
	for _, state := range t.TerminalStates {
		out[state] = struct{}{}
	}
	for from, targets := range t.Transitions {
		out[from] = struct{}{}
		for _, to := range targets {
			out[to] = struct{}{}
		}
	}
	return out
}

func (t TransitionTable[S]) nameOrDefault() string {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return "unnamed"
	}
	return name
}

func CanTransition[S comparable](table TransitionTable[S], from, to S) bool {
	return table.CanTransition(from, to)
}

func ValidTransitions[S comparable](table TransitionTable[S], from S) []S {
	return table.ValidTransitions(from)
}

func ValidTargets[S comparable](table TransitionTable[S], from S) []S {
	return table.ValidTargets(from)
}

func IsTerminal[S comparable](table TransitionTable[S], state S) bool {
	return table.IsTerminal(state)
}
