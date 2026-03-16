package snapshot

import (
	"strings"
	"testing"
)

func TestComputeManifestDigest_NormalizesAndHashesManifest(t *testing.T) {
	digest, raw, err := ComputeManifestDigest(Manifest{
		BaseCommit:          " abc123 ",
		WorkspaceGeneration: " wg-1 ",
		Files: []ManifestFile{
			{Path: "./b.go", Size: 20, Digest: "SHA256:BBBB", Mode: 0o644},
			{Path: "a.go", Size: 10, Digest: "sha256:aaaa", Mode: 0o644},
		},
	})
	if err != nil {
		t.Fatalf("ComputeManifestDigest failed: %v", err)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("unexpected digest: %q", digest)
	}
	if !strings.Contains(raw, `"base_commit":"abc123"`) {
		t.Fatalf("unexpected raw manifest: %s", raw)
	}
	firstIdx := strings.Index(raw, `"path":"a.go"`)
	secondIdx := strings.Index(raw, `"path":"b.go"`)
	if firstIdx < 0 || secondIdx < 0 || firstIdx > secondIdx {
		t.Fatalf("expected sorted file order, raw=%s", raw)
	}
}

func TestValidateManifest_RejectsInvalidManifest(t *testing.T) {
	cases := []Manifest{
		{},
		{
			BaseCommit:          "abc123",
			WorkspaceGeneration: "wg-1",
		},
		{
			BaseCommit:          "abc123",
			WorkspaceGeneration: "wg-1",
			Files:               []ManifestFile{{Path: "../secret", Digest: "sha256:1"}},
		},
		{
			BaseCommit:          "abc123",
			WorkspaceGeneration: "wg-1",
			Files:               []ManifestFile{{Path: "a.go", Digest: "md5:1"}},
		},
	}
	for _, c := range cases {
		if err := ValidateManifest(c); err == nil {
			t.Fatalf("expected manifest to be rejected: %+v", c)
		}
	}
}
