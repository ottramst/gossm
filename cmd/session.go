package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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

	// Get target instance
	target, err := getTargetInstance(ctx)
	if err != nil {
		logErrorAndExit(err)
	}

	// Display information
	internal.PrintReady("start-session", credential.awsConfig.Region, target.Name)

	// Start session
	session, err := createSession(ctx, target.Name)
	if err != nil {
		logErrorAndExit(err)
	}

	// Execute session
	if err := executeSession(session, target.Name); err != nil {
		color.Red("%v", err)
	}

	// Clean up
	if err := terminateSession(ctx, session.SessionId); err != nil {
		logErrorAndExit(err)
	}
}

// createSession creates a new SSM session to the target instance
func createSession(ctx context.Context, targetName string) (*ssm.StartSessionOutput, error) {
	input := &ssm.StartSessionInput{
		Target: aws.String(targetName),
	}

	session, err := internal.CreateStartSession(ctx, *credential.awsConfig, input)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}

// executeSession executes the interactive session using the SSM plugin
func executeSession(session *ssm.StartSessionOutput, targetName string) error {
	// Marshal session to JSON
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// Marshal session parameters to JSON
	paramsJSON, err := json.Marshal(&ssm.StartSessionInput{
		Target: aws.String(targetName),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal session parameters: %w", err)
	}

	// Execute the session
	return internal.CallProcess(
		credential.ssmPluginPath,
		string(sessionJSON),
		credential.awsConfig.Region,
		"StartSession",
		credential.awsProfile,
		string(paramsJSON),
	)
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

	// Bind flags to viper
	viper.BindPFlag("start-session-target", startSessionCommand.Flags().Lookup("target"))

	// Add command to root
	rootCmd.AddCommand(startSessionCommand)
}
