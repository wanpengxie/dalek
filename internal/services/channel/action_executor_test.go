package channel

import "testing"

func TestActionPriorityLabel(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{priority: 0, want: "none(0)"},
		{priority: 1, want: "low(1)"},
		{priority: 2, want: "medium(2)"},
		{priority: 3, want: "high(3)"},
		{priority: 9, want: "9"},
	}
	for _, tc := range cases {
		if got := actionPriorityLabel(tc.priority); got != tc.want {
			t.Fatalf("actionPriorityLabel(%d)=%q, want=%q", tc.priority, got, tc.want)
		}
	}
}
