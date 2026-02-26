package main

import (
	"strings"
	"testing"
)

func TestDispatchDepthGuardState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		raw          string
		wantDepth    int
		wantBlocked  bool
		wantCauseSub string
	}{
		{name: "empty means allow", raw: "", wantDepth: 0, wantBlocked: false},
		{name: "zero means allow", raw: "0", wantDepth: 0, wantBlocked: false},
		{name: "non-zero means block", raw: "2", wantDepth: 2, wantBlocked: true, wantCauseSub: "DALEK_DISPATCH_DEPTH=2"},
		{name: "invalid means block", raw: "x", wantDepth: 0, wantBlocked: true, wantCauseSub: "值非法"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDepth, gotBlocked, gotCause := dispatchDepthGuardState(tc.raw)
			if gotDepth != tc.wantDepth || gotBlocked != tc.wantBlocked {
				t.Fatalf("dispatchDepthGuardState(%q) = (%d,%v,%q), want (%d,%v,...)",
					tc.raw, gotDepth, gotBlocked, gotCause, tc.wantDepth, tc.wantBlocked)
			}
			if tc.wantCauseSub != "" && !strings.Contains(gotCause, tc.wantCauseSub) {
				t.Fatalf("cause should contain %q, got=%q", tc.wantCauseSub, gotCause)
			}
		})
	}
}
