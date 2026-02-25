package pm

import "testing"

func TestParseDispatchDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty", raw: "", want: 0},
		{name: "spaces", raw: "   ", want: 0},
		{name: "zero", raw: "0", want: 0},
		{name: "positive", raw: "7", want: 7},
		{name: "trimmed", raw: " 12 ", want: 12},
		{name: "negative", raw: "-3", want: 0},
		{name: "invalid", raw: "abc", want: 0},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseDispatchDepth(tt.raw); got != tt.want {
				t.Fatalf("parseDispatchDepth(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNextDispatchDepthEnvValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unset", input: "", want: "1"},
		{name: "zero", input: "0", want: "1"},
		{name: "positive", input: "1", want: "2"},
		{name: "trimmed", input: " 5 ", want: "6"},
		{name: "invalid", input: "not-a-number", want: "1"},
		{name: "negative", input: "-1", want: "1"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "unset" {
				t.Setenv(dispatchDepthEnvKey, "")
			} else {
				t.Setenv(dispatchDepthEnvKey, tt.input)
			}
			if got := nextDispatchDepthEnvValue(); got != tt.want {
				t.Fatalf("nextDispatchDepthEnvValue() = %q, want %q", got, tt.want)
			}
		})
	}
}
