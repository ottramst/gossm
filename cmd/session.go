package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/ottramst/gossm/internal"
)

var (
	// startSessionCommand is the Cobra command for starting an SSM session
	startSessionCommand = &cobra.Command{
		Use:   "start",
		Short: "Start an interactive session with an AWS instance",
		Long: `Start an interactive shell session with an AWS instance using AWS Systems Manager Session Manager.

This command establishes a secure session with an EC2 instance without requiring SSH access or
opening inbound ports. It uses the AWS SSM agent running on the target instance.

Escape Sequence:
  Enter ~.   Disconnect from the session (useful when network is stuck)

Example:
  gossm start              # Interactive instance selection
  gossm start -t i-1234    # Connect to a specific instance ID
`,
		Run: runStartSession,
	}
)

// runStartSession executes the start-session operation
func runStartSession(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	flagTarget, _ := cmd.Flags().GetString("target")

	// Get target instance
	target, err := getTargetInstance(ctx, flagTarget)
	if err != nil {
		logErrorAndExit(err)
	}

	// Display information
	internal.PrintReady("start-session", credential.awsConfig.Region, target.Name)

	// Run the interactive session through the SSM plugin
	if err := runPluginSession(ctx, &ssm.StartSessionInput{
		Target: aws.String(target.Name),
	}); err != nil {
		logErrorAndExit(err)
	}
}

// runPluginSession creates an SSM session from the given input, drives the
// session-manager-plugin with it, and terminates the session afterwards.
// It is shared by the start, fwd, and fwdrem commands.
func runPluginSession(ctx context.Context, input *ssm.StartSessionInput) error {
	session, err := internal.CreateStartSession(ctx, *credential.awsConfig, input)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}
	paramsJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal session parameters: %w", err)
	}

	if err := internal.CallProcess(
		credential.ssmPluginPath,
		string(sessionJSON),
		credential.awsConfig.Region,
		"StartSession",
		credential.awsProfile,
		string(paramsJSON),
	); err != nil {
		color.Red("%v", err)
	}

	return terminateSession(ctx, session.SessionId)
}

// terminateSession terminates the SSM session
func terminateSession(ctx context.Context, sessionID *string) error {
	return internal.DeleteStartSession(ctx, *credential.awsConfig, &ssm.TerminateSessionInput{
		SessionId: sessionID,
	})
}

func init() {
	// Define command flags
	startSessionCommand.Flags().StringP("target", "t", "", "Target EC2 instance ID (will prompt if not specified)")

	// Add command to root
	rootCmd.AddCommand(startSessionCommand)
}
