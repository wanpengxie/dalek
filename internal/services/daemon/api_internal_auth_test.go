package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalAPIAuthorize_AcceptsLoopbackRemote(t *testing.T) {
	svc := &InternalAPI{}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "127.0.0.1:34567"
	if err := svc.authorize(req); err != nil {
		t.Fatalf("expected loopback remote to pass, got err=%v", err)
	}
}

func TestInternalAPIAuthorize_RejectsNonLoopbackRemote(t *testing.T) {
	svc := &InternalAPI{}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "10.0.0.5:34567"
	if err := svc.authorize(req); err == nil {
		t.Fatalf("expected non-loopback remote to be rejected")
	}
}

func TestInternalAPIAuthorize_AcceptsRemoteWithinAllowCIDRs(t *testing.T) {
	svc := &InternalAPI{
		cfg: InternalAPIConfig{
			AllowCIDRs: []string{"10.0.0.0/8"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "10.0.0.5:34567"
	if err := svc.authorize(req); err != nil {
		t.Fatalf("expected remote within allow_cidrs to pass, got err=%v", err)
	}
}

func TestValidateInternalListenAddr_RejectsEmptyHost(t *testing.T) {
	if err := validateInternalListenAddr(":18081"); err == nil {
		t.Fatalf("expected empty-host listen to be rejected")
	}
}

func TestValidateInternalListenAddr_AcceptsNonLoopbackListen(t *testing.T) {
	if err := validateInternalListenAddr("0.0.0.0:18081"); err != nil {
		t.Fatalf("expected non-loopback listen to pass, got=%v", err)
	}
}

func TestValidateInternalListenAddr_AcceptsLoopbackListen(t *testing.T) {
	if err := validateInternalListenAddr("127.0.0.1:18081"); err != nil {
		t.Fatalf("expected loopback ipv4 listen to pass, got=%v", err)
	}
	if err := validateInternalListenAddr("[::1]:18081"); err != nil {
		t.Fatalf("expected loopback ipv6 listen to pass, got=%v", err)
	}
}
