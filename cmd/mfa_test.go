package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMFASerialFromIdentity(t *testing.T) {
	tests := []struct {
		name    string
		arn     string
		account string
		want    string
		wantErr bool
	}{
		{
			name:    "IAM user",
			arn:     "arn:aws:iam::123456789012:user/alice",
			account: "123456789012",
			want:    "arn:aws:iam::123456789012:mfa/alice",
		},
		{
			name:    "IAM user with path",
			arn:     "arn:aws:iam::123456789012:user/engineering/bob",
			account: "123456789012",
			want:    "arn:aws:iam::123456789012:mfa/bob",
		},
		{
			name:    "assumed role",
			arn:     "arn:aws:sts::123456789012:assumed-role/admin/session-name",
			account: "123456789012",
			wantErr: true,
		},
		{
			name:    "root identity",
			arn:     "arn:aws:iam::123456789012:root",
			account: "123456789012",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mfaSerialFromIdentity(tt.arn, tt.account)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !strings.Contains(err.Error(), "--device") {
					t.Errorf("error %q should point the user at --device", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("mfaSerialFromIdentity(%q) = %q, want %q", tt.arn, got, tt.want)
			}
		})
	}
}

// Regression test for #28: the MFA credentials file redirect must be
// controllable, so the mfa command can skip it and renew with base
// credentials.
func TestApplyMFACredentialsFile(t *testing.T) {
	oldPath := credentialWithMFA
	t.Cleanup(func() { credentialWithMFA = oldPath })

	mfaFile := filepath.Join(t.TempDir(), "credentials_mfa")
	if err := os.WriteFile(mfaFile, []byte("[default]\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Run("sets env when file exists and env is empty", func(t *testing.T) {
		credentialWithMFA = mfaFile
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
		applyMFACredentialsFile()
		if got := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); got != mfaFile {
			t.Errorf("env = %q, want %q", got, mfaFile)
		}
	})

	t.Run("respects an existing env value", func(t *testing.T) {
		credentialWithMFA = mfaFile
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/custom/path")
		applyMFACredentialsFile()
		if got := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); got != "/custom/path" {
			t.Errorf("env = %q, want untouched /custom/path", got)
		}
	})

	t.Run("no-op when the file does not exist", func(t *testing.T) {
		credentialWithMFA = filepath.Join(t.TempDir(), "missing")
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
		applyMFACredentialsFile()
		if got := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); got != "" {
			t.Errorf("env = %q, want empty", got)
		}
	})
}
