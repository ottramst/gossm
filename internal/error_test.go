package internal

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapErrorNil(t *testing.T) {
	if err := WrapError(nil); err != nil {
		t.Fatalf("WrapError(nil) = %v, want nil", err)
	}
}

func TestWrapError(t *testing.T) {
	base := errors.New("boom")
	err := WrapError(base)
	if err == nil {
		t.Fatal("WrapError returned nil for a non-nil error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("wrapped error %q does not contain the original message", err.Error())
	}
	if !strings.Contains(err.Error(), "TestWrapError") {
		t.Errorf("wrapped error %q does not contain the caller function name", err.Error())
	}
}
