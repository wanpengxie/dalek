package contracts

import "testing"

func TestCanonicalIntegrationStatus(t *testing.T) {
	tests := []struct {
		name string
		in   IntegrationStatus
		want IntegrationStatus
	}{
		{name: "empty", in: "", want: IntegrationNone},
		{name: "none", in: "none", want: IntegrationNone},
		{name: "needs_merge", in: "needs_merge", want: IntegrationNeedsMerge},
		{name: "needs_merge_alias", in: "needs-merge", want: IntegrationNeedsMerge},
		{name: "merged", in: "merged", want: IntegrationMerged},
		{name: "abandoned", in: "abandoned", want: IntegrationAbandoned},
		{name: "abandoned_alias", in: "discarded", want: IntegrationAbandoned},
		{name: "unknown", in: "custom", want: "custom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalIntegrationStatus(tc.in); got != tc.want {
				t.Fatalf("CanonicalIntegrationStatus(%q)=%q, want=%q", tc.in, got, tc.want)
			}
		})
	}
}
