package contracts

import "testing"

func TestParseTicketPriority(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{in: "high", want: TicketPriorityHigh, wantOK: true},
		{in: "MEDIUM", want: TicketPriorityMedium, wantOK: true},
		{in: " low ", want: TicketPriorityLow, wantOK: true},
		{in: "none", want: TicketPriorityNone, wantOK: true},
		{in: "p0", want: 0, wantOK: false},
	}
	for _, tc := range cases {
		got, ok := ParseTicketPriority(tc.in)
		if ok != tc.wantOK {
			t.Fatalf("ParseTicketPriority(%q) ok=%v want=%v", tc.in, ok, tc.wantOK)
		}
		if got != tc.want {
			t.Fatalf("ParseTicketPriority(%q) got=%d want=%d", tc.in, got, tc.want)
		}
	}
}

func TestTicketPriorityLabel(t *testing.T) {
	if got := TicketPriorityLabel(TicketPriorityHigh); got != "high" {
		t.Fatalf("high label mismatch: %q", got)
	}
	if got := TicketPriorityLabel(TicketPriorityMedium); got != "medium" {
		t.Fatalf("medium label mismatch: %q", got)
	}
	if got := TicketPriorityLabel(TicketPriorityLow); got != "low" {
		t.Fatalf("low label mismatch: %q", got)
	}
	if got := TicketPriorityLabel(TicketPriorityNone); got != "none" {
		t.Fatalf("none label mismatch: %q", got)
	}
	if got := TicketPriorityLabel(99); got != "99" {
		t.Fatalf("custom label mismatch: %q", got)
	}
}
