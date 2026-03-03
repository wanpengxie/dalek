package feishudoc

import "testing"

func TestNormalizePermissionTokenType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{name: "default", in: "", want: "docx"},
		{name: "document alias", in: "document", want: "docx"},
		{name: "docx", in: "docx", want: "docx"},
		{name: "invalid", in: "foo", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizePermissionTokenType(tc.in)
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
				t.Fatalf("normalizePermissionTokenType() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestNormalizeLinkShareEntity(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{name: "default", in: "", want: "tenant_editable"},
		{name: "alias", in: "tenant-editable", want: "tenant_editable"},
		{name: "value", in: "anyone_editable", want: "anyone_editable"},
		{name: "invalid", in: "x", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeLinkShareEntity(tc.in)
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
				t.Fatalf("normalizeLinkShareEntity() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestNormalizePermissionMemberType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{name: "default", in: "", want: "openid"},
		{name: "alias", in: "open_id", want: "openid"},
		{name: "email", in: "email", want: "email"},
		{name: "invalid", in: "foo", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizePermissionMemberType(tc.in)
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
				t.Fatalf("normalizePermissionMemberType() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestNormalizePermissionRoleAndType(t *testing.T) {
	perm, err := normalizePermissionRole("")
	if err != nil {
		t.Fatalf("normalizePermissionRole() default error: %v", err)
	}
	if perm != "edit" {
		t.Fatalf("normalizePermissionRole() default mismatch: %q", perm)
	}

	permType, err := normalizePermissionRoleType("")
	if err != nil {
		t.Fatalf("normalizePermissionRoleType() default error: %v", err)
	}
	if permType != "container" {
		t.Fatalf("normalizePermissionRoleType() default mismatch: %q", permType)
	}

	if _, err := normalizePermissionRole("owner"); err == nil {
		t.Fatalf("normalizePermissionRole() expected error for invalid value")
	}
	if _, err := normalizePermissionRoleType("all"); err == nil {
		t.Fatalf("normalizePermissionRoleType() expected error for invalid value")
	}
}

func TestTokenURL(t *testing.T) {
	svc := &Service{baseURL: "https://open.feishu.cn"}
	tests := []struct {
		name      string
		tokenType string
		token     string
		want      string
	}{
		{name: "docx", tokenType: "docx", token: "doxc123", want: "https://open.feishu.cn/docx/doxc123"},
		{name: "wiki", tokenType: "wiki", token: "wikc123", want: "https://open.feishu.cn/wiki/wikc123"},
		{name: "unknown", tokenType: "x", token: "abc", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := svc.tokenURL(tc.tokenType, tc.token)
			if got != tc.want {
				t.Fatalf("tokenURL() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}
