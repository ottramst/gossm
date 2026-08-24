package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"

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

	flagTarget, _ := cmd.Flags().GetString("target")
	flagRemote, _ := cmd.Flags().GetString("remote")
	flagLocal, _ := cmd.Flags().GetString("local")
	flagHost, _ := cmd.Flags().GetString("host")

	// Get target instance to proxy through
	target, err := getTargetInstance(ctx, flagTarget)
	if err != nil {
		logErrorAndExit(err)
	}

	// Get port configuration
	localPort, remotePort, err := portConfiguration(flagRemote, flagLocal)
	if err != nil {
		logErrorAndExit(err)
	}

	// Get remote host to connect to
	host, err := getRemoteHost(flagHost)
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

// getRemoteHost determines the remote host to connect to, using the given
// flag value or prompting interactively when it is empty
func getRemoteHost(flagHost string) (string, error) {
	host := strings.TrimSpace(flagHost)
	if host != "" {
		return host, nil
	}

	// If no host specified, prompt user
	return internal.AskHost()
}

// startRemoteHostPortForwardingSession creates and starts an SSM port forwarding session to a remote host
func startRemoteHostPortForwardingSession(ctx context.Context, target *internal.Target, localPort, remotePort, host string) error {
	return runPluginSession(ctx, &ssm.StartSessionInput{
		DocumentName: aws.String(documentNameRemotePortForwarding),
		Parameters: map[string][]string{
			"portNumber":      {remotePort},
			"localPortNumber": {localPort},
			"host":            {host},
		},
		Target: aws.String(target.Name),
	})
}

// init initializes the fwdrem command
func init() {
	// Define command flags
	fwdremCommand.Flags().StringP("remote", "z", "", "Remote port on the target host to forward to (e.g., 8080)")
	fwdremCommand.Flags().StringP("local", "l", "", "Local port to use (defaults to remote port if not specified)")
	fwdremCommand.Flags().StringP("target", "t", "", "AWS EC2 instance to proxy through (will prompt if not specified)")
	fwdremCommand.Flags().StringP("host", "a", "", "Remote host address to connect to (e.g., internal-db)")

	// Add command to root
	rootCmd.AddCommand(fwdremCommand)
}
