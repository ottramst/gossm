package cmd

import "testing"

func TestPortConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		local      string
		wantLocal  string
		wantRemote string
	}{
		{"both set", "8080", "9090", "9090", "8080"},
		{"local defaults to remote", "8080", "", "8080", "8080"},
		{"values are trimmed", " 8080 ", " 9090 ", "9090", "8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local, remote, err := portConfiguration(tt.remote, tt.local)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if local != tt.wantLocal || remote != tt.wantRemote {
				t.Errorf("portConfiguration(%q, %q) = (%q, %q), want (%q, %q)",
					tt.remote, tt.local, local, remote, tt.wantLocal, tt.wantRemote)
			}
		})
	}
}

// Regression test for the viper key collision (#27): fwd and fwdrem used to
// bind the same viper keys, so fwd's flags were silently ignored, and start's
// -t flag was bound to a key nobody read. Each command must see exactly the
// values set on its own flag set.
func TestCommandFlagsAreIndependent(t *testing.T) {
	if err := fwdCommand.Flags().Set("remote", "1111"); err != nil {
		t.Fatal(err)
	}
	if err := fwdremCommand.Flags().Set("remote", "2222"); err != nil {
		t.Fatal(err)
	}
	if err := startSessionCommand.Flags().Set("target", "i-abc"); err != nil {
		t.Fatal(err)
	}

	if got, _ := fwdCommand.Flags().GetString("remote"); got != "1111" {
		t.Errorf("fwd -z = %q, want %q", got, "1111")
	}
	if got, _ := fwdremCommand.Flags().GetString("remote"); got != "2222" {
		t.Errorf("fwdrem -z = %q, want %q", got, "2222")
	}
	if got, _ := startSessionCommand.Flags().GetString("target"); got != "i-abc" {
		t.Errorf("start -t = %q, want %q", got, "i-abc")
	}
}
