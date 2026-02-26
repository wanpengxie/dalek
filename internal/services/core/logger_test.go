package core

import "testing"

func TestDefaultLogger_NotNil(t *testing.T) {
	logger := DefaultLogger()
	if logger == nil {
		t.Fatalf("DefaultLogger should not be nil")
	}
	logger.Info("default logger smoke")
}

func TestEnsureLogger_Fallback(t *testing.T) {
	logger := EnsureLogger(nil)
	if logger == nil {
		t.Fatalf("EnsureLogger(nil) should return fallback logger")
	}
	logger.Info("fallback logger smoke")
}

func TestDiscardLogger_NotNil(t *testing.T) {
	logger := DiscardLogger()
	if logger == nil {
		t.Fatalf("DiscardLogger should not be nil")
	}
	logger.Info("discard logger smoke")
}
