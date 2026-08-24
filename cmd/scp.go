package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"

	"github.com/ottramst/gossm/internal"
)

const (
	// documentNameSSH is the SSM document for SSH sessions
	documentNameSSH = "AWS-StartSSHSession"

	// defaultSSHPort is the default port for SSH connections
	defaultSSHPort = "22"
)

var (
	// scpCommand is the Cobra command for SCP file transfers via SSM
	scpCommand = &cobra.Command{
		Use:   "scp",
		Short: "Transfer files using SCP via AWS Systems Manager",
		Long: `Transfer files between your local machine and AWS instances using SCP 
through AWS Systems Manager Session Manager.

This command establishes an SCP connection through SSM, allowing secure file
transfers without requiring direct SSH access to the instance.

Escape Sequence:
  Enter ~.   Disconnect from the session (useful when network is stuck)

Example:
  gossm scp --exec "-i key.pem file.txt ec2-user@instance:/home/ec2-user/"
`,
		Run: runSCPCommand,
	}
)

// runSCPCommand executes the SCP file transfer operation
func runSCPCommand(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	flagExec, _ := cmd.Flags().GetString("exec")

	// Get and validate SCP command arguments
	scpArgs, err := validateSCPArguments(flagExec)
	if err != nil {
		logErrorAndExit(err)
	}

	// Parse source and destination to find the target instance
	targetInstanceID, err := findTargetInstanceID(ctx, scpArgs)
	if err != nil {
		logErrorAndExit(err)
	}

	// Display information about the command
	displaySCPCommandInfo(scpArgs, targetInstanceID)

	// Start an SSH session through SSM
	session, err := startSSHSession(ctx, targetInstanceID)
	if err != nil {
		logErrorAndExit(err)
	}

	// Execute SCP command with SSM as proxy
	err = runProxiedCommand("scp", scpArgs, session, sshSessionInput(targetInstanceID))
	if err != nil {
		color.Red("%v", err)
	}

	// Clean up by terminating the session
	err = terminateSession(ctx, session.SessionId)
	if err != nil {
		logErrorAndExit(err)
	}
}

// validateSCPArguments validates and parses the SCP command arguments
func validateSCPArguments(flagExec string) (string, error) {
	scpArgs := strings.TrimSpace(flagExec)

	if scpArgs == "" {
		return "", errors.New("SCP command arguments are required")
	}

	// Basic validation of SCP arguments, respecting quoted segments
	parts, err := shellquote.Split(scpArgs)
	if err != nil {
		return "", fmt.Errorf("invalid SCP arguments: %w", err)
	}
	if len(parts) < 2 {
		return "", errors.New("invalid SCP arguments: must include source and destination")
	}

	return scpArgs, nil
}

// findTargetInstanceID identifies the instance ID for the SCP operation
func findTargetInstanceID(ctx context.Context, scpArgs string) (string, error) {
	parts, err := shellquote.Split(scpArgs)
	if err != nil {
		return "", fmt.Errorf("invalid SCP arguments: %w", err)
	}

	hostname, err := hostFromSCPArgs(parts)
	if err != nil {
		return "", err
	}

	return resolveTargetHost(ctx, hostname)
}

// hostFromSCPArgs extracts the remote host (user@host:path) from parsed scp
// arguments, checking the destination first and then the source.
func hostFromSCPArgs(parts []string) (string, error) {
	if len(parts) < 2 {
		return "", errors.New("invalid SCP arguments: must include source and destination")
	}
	for _, arg := range []string{parts[len(parts)-1], parts[len(parts)-2]} {
		segs := strings.Split(arg, ":")
		if len(segs) < 2 {
			continue
		}
		hostParts := strings.Split(segs[0], "@")
		if len(hostParts) == 2 && hostParts[1] != "" {
			return hostParts[1], nil
		}
	}
	return "", errors.New("could not identify target hostname in SCP arguments")
}

// displaySCPCommandInfo shows information about the SCP operation
func displaySCPCommandInfo(scpArgs, targetInstanceID string) {
	internal.PrintReady("scp", credential.awsConfig.Region, targetInstanceID)
	color.Cyan("scp %s", scpArgs)
}

// sshSessionInput builds the SSM session input for tunneling SSH (and SCP)
// to the given instance.
func sshSessionInput(targetInstanceID string) *ssm.StartSessionInput {
	return &ssm.StartSessionInput{
		DocumentName: aws.String(documentNameSSH),
		Parameters:   map[string][]string{"portNumber": {defaultSSHPort}},
		Target:       aws.String(targetInstanceID),
	}
}

// startSSHSession starts an SSH session through SSM
func startSSHSession(ctx context.Context, targetInstanceID string) (*ssm.StartSessionOutput, error) {
	session, err := internal.CreateStartSession(ctx, *credential.awsConfig, sshSessionInput(targetInstanceID))
	if err != nil {
		return nil, fmt.Errorf("failed to create SSM session: %w", err)
	}

	return session, nil
}

func init() {
	// Define command flags
	scpCommand.Flags().StringP("exec", "e", "", "SCP command arguments (e.g., \"-r localfile user@instance:/remote/path\")")
	_ = scpCommand.MarkFlagRequired("exec")

	// Add command to root
	rootCmd.AddCommand(scpCommand)
}
