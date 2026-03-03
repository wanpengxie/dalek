package feishudoc

import "testing"

func TestNormalizeWikiPageSize(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "default", in: 0, want: 50},
		{name: "normal", in: 20, want: 20},
		{name: "max", in: 500, want: 200},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeWikiPageSize(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeWikiPageSize() mismatch: got=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestNormalizeWikiObjType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{name: "default", in: "", want: "docx"},
		{name: "docx", in: "docx", want: "docx"},
		{name: "wiki", in: "wiki", want: "wiki"},
		{name: "invalid", in: "foo", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeWikiObjType(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("normalizeWikiObjType() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}
