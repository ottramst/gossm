package internal

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeRecorder is an io.WriteCloser that records everything written to it.
type writeRecorder struct {
	bytes.Buffer
	closed bool
}

func (w *writeRecorder) Close() error {
	w.closed = true
	return nil
}

func runEscapeCopy(t *testing.T, input string) (output string, escaped bool) {
	t.Helper()

	dst := &writeRecorder{}
	escapeDetected := make(chan bool, 1)

	err := copyWithEscapeDetection(context.Background(), dst, strings.NewReader(input), escapeDetected)
	if err != nil {
		t.Fatalf("copyWithEscapeDetection(%q) returned error: %v", input, err)
	}
	if !dst.closed {
		t.Errorf("copyWithEscapeDetection(%q) did not close the destination", input)
	}

	select {
	case <-escapeDetected:
		escaped = true
	default:
	}

	return dst.String(), escaped
}

func TestCopyWithEscapeDetection(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantOutput  string
		wantEscaped bool
	}{
		{"plain text", "hello", "hello", false},
		{"escape at start", "~.", "", true},
		{"escape after newline", "line\n~.", "line\n", true},
		{"double tilde is literal", "~~ok", "~~ok", false},
		{"tilde plus other char", "~x", "~x", false},
		{"mid-line tilde dot is literal", "a~.", "a~.", false},
		{"carriage return counts as newline", "a\r~.", "a\r", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, escaped := runEscapeCopy(t, tt.input)
			if output != tt.wantOutput {
				t.Errorf("output = %q, want %q", output, tt.wantOutput)
			}
			if escaped != tt.wantEscaped {
				t.Errorf("escaped = %v, want %v", escaped, tt.wantEscaped)
			}
		})
	}
}

func TestIsWaitError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("wait: no child processes"), true},
		{errors.New("waitid: no child processes"), true},
		{errors.New("some other error"), false},
	}

	for _, tt := range tests {
		if got := isWaitError(tt.err); got != tt.want {
			t.Errorf("isWaitError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestTerminateGracefully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX signals")
	}

	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- terminateGracefully(cmd) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("terminateGracefully returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("terminateGracefully did not return in time")
	}
}
