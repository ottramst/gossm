package cmd

import (
	"testing"

	"github.com/spf13/viper"
)

func TestGetAWSProfile(t *testing.T) {
	t.Cleanup(func() { viper.Set("profile", "") })

	t.Run("default when nothing is set", func(t *testing.T) {
		viper.Set("profile", "")
		t.Setenv("AWS_PROFILE", "")
		if got := getAWSProfile(); got != defaultProfile {
			t.Errorf("getAWSProfile() = %q, want %q", got, defaultProfile)
		}
	})

	t.Run("environment variable", func(t *testing.T) {
		viper.Set("profile", "")
		t.Setenv("AWS_PROFILE", "from-env")
		if got := getAWSProfile(); got != "from-env" {
			t.Errorf("getAWSProfile() = %q, want %q", got, "from-env")
		}
	})

	t.Run("flag beats environment", func(t *testing.T) {
		viper.Set("profile", "from-flag")
		t.Setenv("AWS_PROFILE", "from-env")
		if got := getAWSProfile(); got != "from-flag" {
			t.Errorf("getAWSProfile() = %q, want %q", got, "from-flag")
		}
	})
}
