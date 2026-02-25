package main

import (
	"testing"
)

func TestBuildGatewaySendAPIURL_FromListenHostPort(t *testing.T) {
	got, err := buildGatewaySendAPIURL("127.0.0.1:28080")
	if err != nil {
		t.Fatalf("buildGatewaySendAPIURL failed: %v", err)
	}
	if got != "http://127.0.0.1:28080/api/send" {
		t.Fatalf("unexpected endpoint: %q", got)
	}
}

func TestBuildGatewaySendAPIURL_FromHTTPSURL(t *testing.T) {
	got, err := buildGatewaySendAPIURL("https://gateway.example.com/base")
	if err != nil {
		t.Fatalf("buildGatewaySendAPIURL failed: %v", err)
	}
	if got != "https://gateway.example.com/base/api/send" {
		t.Fatalf("unexpected endpoint: %q", got)
	}
}
