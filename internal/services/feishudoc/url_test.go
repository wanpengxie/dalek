package feishudoc

import "testing"

func TestParseURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantToken string
		wantType  string
		wantErr   bool
	}{
		{name: "docx", url: "https://feishu.cn/docx/CHtVdl7cXoPs0ExLU7Kcwlcqnvh", wantToken: "CHtVdl7cXoPs0ExLU7Kcwlcqnvh", wantType: "docx"},
		{name: "wiki", url: "https://feishu.cn/wiki/wikc123abc", wantToken: "wikc123abc", wantType: "wiki"},
		{name: "sheet", url: "https://feishu.cn/sheets/sht123", wantToken: "sht123", wantType: "sheet"},
		{name: "docs", url: "https://feishu.cn/docs/doc123", wantToken: "doc123", wantType: "doc"},
		{name: "subdomain", url: "https://mycompany.feishu.cn/docx/abc123", wantToken: "abc123", wantType: "docx"},
		{name: "larksuite", url: "https://mycompany.larksuite.com/docx/abc123", wantToken: "abc123", wantType: "docx"},
		{name: "with query", url: "https://feishu.cn/docx/abc123?from=share", wantToken: "abc123", wantType: "docx"},
		{name: "with fragment", url: "https://feishu.cn/docx/abc123#heading", wantToken: "abc123", wantType: "docx"},
		{name: "base", url: "https://feishu.cn/base/bitable123", wantToken: "bitable123", wantType: "bitable"},
		{name: "slides", url: "https://feishu.cn/slides/slides123", wantToken: "slides123", wantType: "slides"},
		{name: "empty", url: "", wantErr: true},
		{name: "not feishu", url: "https://google.com/docx/abc", wantErr: true},
		{name: "no token", url: "https://feishu.cn/docx/", wantErr: true},
		{name: "no path", url: "https://feishu.cn/", wantErr: true},
		{name: "unknown type", url: "https://feishu.cn/unknown/abc", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseURL(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Token != tc.wantToken {
				t.Fatalf("token mismatch: got=%q want=%q", got.Token, tc.wantToken)
			}
			if got.TokenType != tc.wantType {
				t.Fatalf("type mismatch: got=%q want=%q", got.TokenType, tc.wantType)
			}
		})
	}
}
