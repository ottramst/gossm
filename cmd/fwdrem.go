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
	"github.com/spf13/viper"

	"github.com/ottramst/gossm/internal"
)

const (
	// documentNameRemotePortForwarding is the SSM document used for remote host port forwarding
	documentNameRemotePortForwarding = "AWS-StartPortForwardingSessionToRemoteHost"
)

var (
	// fwdremCommand is the Cobra command for SSM port forwarding to a remote host
	fwdremCommand = &cobra.Command{
		Use:   "fwdrem",
		Short: "Forward ports to a remote host through an AWS instance",
		Long:  "Create port forwarding tunnels to a remote host through an AWS instance using AWS Systems Manager",
		Run:   runRemotePortForwarding,
	}
)

// runRemotePortForwarding executes the remote host port forwarding operation
func runRemotePortForwarding(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	// Get target instance to proxy through
	target, err := getProxyInstance(ctx)
	if err != nil {
		logErrorAndExit(err)
	}

	// Get port configuration
	localPort, remotePort, err := GetPortConfiguration()
	if err != nil {
		logErrorAndExit(err)
	}

	// Get remote host to connect to
	host, err := getRemoteHost()
	if err != nil {
		logErrorAndExit(err)
	}

	// Display information about the port forwarding
	internal.PrintReady(
		fmt.Sprintf("start-port-forwarding %s -> %s:%s", localPort, host, remotePort),
		credential.awsConfig.Region,
		target.Name,
	)

	// Create and start the forwarding session
	if err := startRemoteHostPortForwardingSession(ctx, target, localPort, remotePort, host); err != nil {
		logErrorAndExit(err)
	}
}

// getProxyInstance retrieves the target instance to proxy through
func getProxyInstance(ctx context.Context) (*internal.Target, error) {
	// Check if target was specified via command line
	argTarget := strings.TrimSpace(viper.GetString("fwd-target"))
	if argTarget != "" {
		return findSpecificProxyInstance(ctx, argTarget)
	}

	// If no target specified, prompt user to select
	return internal.AskTarget(ctx, *credential.awsConfig)
}

// findSpecificProxyInstance looks for a specific instance by name
func findSpecificProxyInstance(ctx context.Context, targetName string) (*internal.Target, error) {
	instances, err := internal.FindInstances(ctx, *credential.awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to find instances: %w", err)
	}

	for _, instance := range instances {
		if instance.Name == targetName {
			return instance, nil
		}
	}

	return nil, fmt.Errorf("proxy instance '%s' not found", targetName)
}

// getRemoteHost determines the remote host to connect to
func getRemoteHost() (string, error) {
	// Check if host was specified via command line
	host := strings.TrimSpace(viper.GetString("fwd-host"))
	if host != "" {
		return host, nil
	}

	// If no host specified, prompt user
	return internal.AskHost()
}

// startRemoteHostPortForwardingSession creates and starts an SSM port forwarding session to a remote host
func startRemoteHostPortForwardingSession(ctx context.Context, target *internal.Target, localPort, remotePort, host string) error {
	// Prepare SSM input for port forwarding
	sessionInput := &ssm.StartSessionInput{
		DocumentName: aws.String(documentNameRemotePortForwarding),
		Parameters: map[string][]string{
			"portNumber":      {remotePort},
			"localPortNumber": {localPort},
			"host":            {host},
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

// init initializes the fwdrem command
func init() {
	// Define command flags
	fwdremCommand.Flags().StringP("remote", "z", "", "Remote port on the target host to forward to (e.g., 8080)")
	fwdremCommand.Flags().StringP("local", "l", "", "Local port to use (defaults to remote port if not specified)")
	fwdremCommand.Flags().StringP("target", "t", "", "AWS EC2 instance to proxy through (will prompt if not specified)")
	fwdremCommand.Flags().StringP("host", "a", "", "Remote host address to connect to (e.g., internal-db)")

	// Bind flags to viper
	viper.BindPFlag("fwd-remote-port", fwdremCommand.Flags().Lookup("remote"))
	viper.BindPFlag("fwd-local-port", fwdremCommand.Flags().Lookup("local"))
	viper.BindPFlag("fwd-target", fwdremCommand.Flags().Lookup("target"))
	viper.BindPFlag("fwd-host", fwdremCommand.Flags().Lookup("host"))

	// Add command to root
	rootCmd.AddCommand(fwdremCommand)
}
