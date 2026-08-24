package cmd

import (
	"context"
	"testing"

	"github.com/ottramst/gossm/internal"
)

func TestHostFromSSHArgs(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		want    string
		wantErr bool
	}{
		{"instance id", []string{"ec2-user@i-1234567890abcdef0"}, "i-1234567890abcdef0", false},
		{"hostname with flags", []string{"-i", "key.pem", "user@host.example.com"}, "host.example.com", false},
		{"no user prefix", []string{"justhost"}, "", true},
		{"empty host", []string{"user@"}, "", true},
		{"empty args", []string{}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hostFromSSHArgs(tt.parts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("hostFromSSHArgs(%v) error = %v, wantErr %v", tt.parts, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("hostFromSSHArgs(%v) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}

func TestHostFromSCPArgs(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		want    string
		wantErr bool
	}{
		{"remote destination", []string{"file.txt", "user@i-123:/home/user/"}, "i-123", false},
		{"remote source", []string{"user@host.internal:/etc/config", "local.txt"}, "host.internal", false},
		{"with flags before paths", []string{"-r", "dir", "user@i-456:/data"}, "i-456", false},
		{"no remote side", []string{"a.txt", "b.txt"}, "", true},
		{"windows-style path is not a host", []string{"C:\\file.txt", "dest.txt"}, "", true},
		{"too few args", []string{"only-one"}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hostFromSCPArgs(tt.parts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("hostFromSCPArgs(%v) error = %v, wantErr %v", tt.parts, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("hostFromSCPArgs(%v) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}

// Regression test for #29: instance IDs must be used directly, never DNS
// resolved (the documented `gossm ssh -e "user@i-..."` form used to fail
// with a DNS error).
func TestResolveTargetHostInstanceID(t *testing.T) {
	for _, id := range []string{"i-1234567890abcdef0", "mi-0123456789abcdef0"} {
		got, err := resolveTargetHost(context.Background(), id)
		if err != nil {
			t.Fatalf("resolveTargetHost(%q) returned error: %v", id, err)
		}
		if got != id {
			t.Errorf("resolveTargetHost(%q) = %q, want the ID unchanged", id, got)
		}
	}
}

func TestSSHHostForTarget(t *testing.T) {
	withPublic := &internal.Target{Name: "i-1", PublicDomain: "pub.example.com"}
	if got := sshHostForTarget(withPublic); got != "pub.example.com" {
		t.Errorf("sshHostForTarget = %q, want public DNS name", got)
	}

	privateOnly := &internal.Target{Name: "i-2"}
	if got := sshHostForTarget(privateOnly); got != "i-2" {
		t.Errorf("sshHostForTarget = %q, want instance ID fallback", got)
	}
}
