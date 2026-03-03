package feishudoc

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty uses default",
			in:   "",
			want: "https://open.feishu.cn",
		},
		{
			name: "trim spaces and slash",
			in:   "  https://open.feishu.cn/  ",
			want: "https://open.feishu.cn",
		},
		{
			name: "custom host",
			in:   "https://foo.example.com////",
			want: "https://foo.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeBaseURL(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeBaseURL() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestNew_ValidateConfig(t *testing.T) {
	_, err := New(Config{
		AppID:     "",
		AppSecret: "secret",
	})
	if err == nil {
		t.Fatalf("expected error when app_id is empty")
	}

	_, err = New(Config{
		AppID:     "app-id",
		AppSecret: "",
	})
	if err == nil {
		t.Fatalf("expected error when app_secret is empty")
	}
}

func TestNew_NormalizedFields(t *testing.T) {
	svc, err := New(Config{
		AppID:     " app-id ",
		AppSecret: " secret ",
		BaseURL:   " https://open.feishu.cn/ ",
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if svc.appID != "app-id" {
		t.Fatalf("unexpected appID: %q", svc.appID)
	}
	if svc.appSecret != "secret" {
		t.Fatalf("unexpected appSecret: %q", svc.appSecret)
	}
	if svc.baseURL != "https://open.feishu.cn" {
		t.Fatalf("unexpected baseURL: %q", svc.baseURL)
	}
	if svc.client == nil {
		t.Fatalf("client should not be nil")
	}
}
