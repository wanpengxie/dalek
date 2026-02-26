package agentexec

import (
	"strings"
	"testing"
)

func TestShellQuote_StripsControlCharacters(t *testing.T) {
	got := shellQuote("a\nb\rc\x00d")
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") || strings.Contains(got, "\x00") {
		t.Fatalf("quoted arg should not contain control chars: %q", got)
	}
	if got != "'a b cd'" {
		t.Fatalf("unexpected quoted arg: %q", got)
	}
}

func TestShellJoin_StripsControlCharacters(t *testing.T) {
	got := shellJoin("bash", []string{"-lc", "echo a\nb"})
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") || strings.Contains(got, "\x00") {
		t.Fatalf("shell join result should not contain control chars: %q", got)
	}
	if !strings.Contains(got, "'echo a b'") {
		t.Fatalf("expected sanitized arg in shell join: %q", got)
	}
}
