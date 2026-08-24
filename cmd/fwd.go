package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/ottramst/gossm/internal"
)

const (
	// documentNamePortForwarding is the SSM document used for port forwarding
	documentNamePortForwarding = "AWS-StartPortForwardingSession"
)

var (
	// fwdCommand is the Cobra command for SSM port forwarding
	fwdCommand = &cobra.Command{
		Use:   "fwd",
		Short: "Forward ports from local machine to remote AWS instances",
		Long:  "Create port forwarding tunnels from your local machine to AWS instances using AWS Systems Manager",
		Run:   runPortForwarding,
	}
)

// runPortForwarding executes the port forwarding operation
func runPortForwarding(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	flagTarget, _ := cmd.Flags().GetString("target")
	flagRemote, _ := cmd.Flags().GetString("remote")
	flagLocal, _ := cmd.Flags().GetString("local")

	// Get target instance
	target, err := getTargetInstance(ctx, flagTarget)
	if err != nil {
		logErrorAndExit(err)
	}

	// Get port configuration
	localPort, remotePort, err := portConfiguration(flagRemote, flagLocal)
	if err != nil {
		logErrorAndExit(err)
	}

	// Display information about the port forwarding
	internal.PrintReady(
		fmt.Sprintf("start-port-forwarding %s -> %s", localPort, remotePort),
		credential.awsConfig.Region,
		target.Name,
	)

	// Create and start the forwarding session
	if err := startPortForwardingSession(ctx, target, localPort, remotePort); err != nil {
		logErrorAndExit(err)
	}
}

// getTargetInstance retrieves the target instance, using the given flag value
// or prompting interactively when it is empty
func getTargetInstance(ctx context.Context, flagTarget string) (*internal.Target, error) {
	argTarget := strings.TrimSpace(flagTarget)
	if argTarget != "" {
		return findSpecificInstance(ctx, argTarget)
	}

	// If no target specified, prompt user to select
	return internal.AskTarget(ctx, *credential.awsConfig)
}

// findSpecificInstance looks for a specific instance by ID
func findSpecificInstance(ctx context.Context, targetName string) (*internal.Target, error) {
	instances, err := internal.FindInstances(ctx, *credential.awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to find instances: %w", err)
	}

	for _, instance := range instances {
		if instance.Name == targetName {
			return instance, nil
		}
	}

	return nil, fmt.Errorf("target instance '%s' not found", targetName)
}

// portConfiguration determines the local and remote ports for forwarding,
// prompting interactively when the remote port flag is empty
func portConfiguration(flagRemote, flagLocal string) (localPort, remotePort string, err error) {
	remotePort = strings.TrimSpace(flagRemote)
	localPort = strings.TrimSpace(flagLocal)

	if remotePort == "" {
		// If not specified, prompt user for ports
		ports, err := internal.AskPorts()
		if err != nil {
			return "", "", fmt.Errorf("failed to get port configuration: %w", err)
		}
		remotePort = ports.Remote
		localPort = ports.Local
	} else if localPort == "" {
		// If remote port is specified but local port isn't, use the same port
		localPort = remotePort
	}

	return localPort, remotePort, nil
}

// startPortForwardingSession creates and starts an SSM port forwarding session
func startPortForwardingSession(ctx context.Context, target *internal.Target, localPort, remotePort string) error {
	// Prepare SSM input for port forwarding
	sessionInput := &ssm.StartSessionInput{
		DocumentName: aws.String(documentNamePortForwarding),
		Parameters: map[string][]string{
			"portNumber":      {remotePort},
			"localPortNumber": {localPort},
		},
		Target: aws.String(target.Name),
	}

	// Create the session
	session, err := internal.CreateStartSession(ctx, *credential.awsConfig, sessionInput)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Marshal session and parameters to JSON for the SSM plugin
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	paramsJSON, err := json.Marshal(sessionInput)
	if err != nil {
		return fmt.Errorf("failed to marshal parameters: %w", err)
	}

	// Call the SSM plugin to start the port forwarding
	if err := internal.CallProcess(
		credential.ssmPluginPath,
		string(sessionJSON),
		credential.awsConfig.Region,
		"StartSession",
		credential.awsProfile,
		string(paramsJSON),
	); err != nil {
		color.Red("[err] %v", err.Error())
	}

	// Clean up by terminating the session
	if err := internal.DeleteStartSession(ctx, *credential.awsConfig, &ssm.TerminateSessionInput{
		SessionId: session.SessionId,
	}); err != nil {
		return fmt.Errorf("failed to terminate session: %w", err)
	}

	return nil
}

func init() {
	// Define command flags
	fwdCommand.Flags().StringP("remote", "z", "", "Remote port to forward to (e.g., 8080)")
	fwdCommand.Flags().StringP("local", "l", "", "Local port to use (defaults to remote port if not specified)")
	fwdCommand.Flags().StringP("target", "t", "", "Target EC2 instance ID (will prompt if not specified)")

	// Add command to root
	rootCmd.AddCommand(fwdCommand)
}
