package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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

	// Get and validate SCP command arguments
	scpArgs, err := validateSCPArguments()
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
	err = executeSCPCommand(scpArgs, session, targetInstanceID)
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
func validateSCPArguments() (string, error) {
	scpArgs := strings.TrimSpace(viper.GetString("scp-exec"))

	if scpArgs == "" {
		return "", fmt.Errorf("SCP command arguments are required")
	}

	// Basic validation of SCP arguments
	parts := strings.Split(scpArgs, " ")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid SCP arguments: must include source and destination")
	}

	return scpArgs, nil
}

// findTargetInstanceID identifies the instance ID for the SCP operation
func findTargetInstanceID(ctx context.Context, scpArgs string) (string, error) {
	// Split the arguments to identify source and destination
	parts := strings.Split(scpArgs, " ")
	dst := parts[len(parts)-1]
	src := parts[len(parts)-2]

	// Parse to find the host part (could be in source or destination)
	var hostname string

	// Check destination for hostname (user@host:path format)
	dstParts := strings.Split(dst, ":")
	if len(dstParts) > 1 {
		hostParts := strings.Split(dstParts[0], "@")
		if len(hostParts) == 2 {
			hostname = hostParts[1]
		}
	}

	// If not found in destination, check source
	if hostname == "" {
		srcParts := strings.Split(src, ":")
		if len(srcParts) > 1 {
			hostParts := strings.Split(srcParts[0], "@")
			if len(hostParts) == 2 {
				hostname = hostParts[1]
			}
		}
	}

	if hostname == "" {
		return "", fmt.Errorf("could not identify target hostname in SCP arguments")
	}

	// Resolve hostname to IP address
	ips, err := net.LookupIP(hostname)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("failed to resolve hostname '%s': %w", hostname, err)
	}

	// Find instance ID based on IP address
	ip := ips[0].String()
	instanceID, err := internal.FindInstanceIdByIp(ctx, *credential.awsConfig, ip)
	if err != nil {
		return "", fmt.Errorf("failed to find instance by IP '%s': %w", ip, err)
	}

	if instanceID == "" {
		return "", fmt.Errorf("no matching instance found for IP '%s'", ip)
	}

	return instanceID, nil
}

// displaySCPCommandInfo shows information about the SCP operation
func displaySCPCommandInfo(scpArgs, targetInstanceID string) {
	internal.PrintReady("scp", credential.awsConfig.Region, targetInstanceID)
	color.Cyan("scp %s", scpArgs)
}

// startSSHSession starts an SSH session through SSM
func startSSHSession(ctx context.Context, targetInstanceID string) (*ssm.StartSessionOutput, error) {
	input := &ssm.StartSessionInput{
		DocumentName: aws.String(documentNameSSH),
		Parameters:   map[string][]string{"portNumber": {defaultSSHPort}},
		Target:       aws.String(targetInstanceID),
	}

	session, err := internal.CreateStartSession(ctx, *credential.awsConfig, input)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSM session: %w", err)
	}

	return session, nil
}

// executeSCPCommand executes the SCP command with SSM as proxy
func executeSCPCommand(scpArgs string, session *ssm.StartSessionOutput, targetInstanceID string) error {
	// Marshal session information to JSON
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	// Create parameter input for the SSM plugin
	input := &ssm.StartSessionInput{
		DocumentName: aws.String(documentNameSSH),
		Parameters:   map[string][]string{"portNumber": {defaultSSHPort}},
		Target:       aws.String(targetInstanceID),
	}

	// Marshal parameters to JSON
	paramsJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal session parameters: %w", err)
	}

	// Build proxy command for SCP
	proxyCommand := fmt.Sprintf("ProxyCommand=%s '%s' %s %s %s '%s'",
		credential.ssmPluginPath,
		string(sessionJSON),
		credential.awsConfig.Region,
		"StartSession",
		credential.awsProfile,
		string(paramsJSON),
	)

	// Build SCP command arguments
	args := []string{"-o", proxyCommand}
	for _, arg := range strings.Fields(scpArgs) {
		if arg != "" {
			args = append(args, arg)
		}
	}

	// Execute SCP command
	return internal.CallProcess("scp", args...)
}

func init() {
	// Define command flags
	scpCommand.Flags().StringP("exec", "e", "", "SCP command arguments (e.g., \"-r localfile user@instance:/remote/path\")")
	scpCommand.MarkFlagRequired("exec")

	// Bind flags to viper
	viper.BindPFlag("scp-exec", scpCommand.Flags().Lookup("exec"))

	// Add command to root
	rootCmd.AddCommand(scpCommand)
}
