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

var (
	// sshCommand is the Cobra command for SSH via SSM
	sshCommand = &cobra.Command{
		Use:   "ssh",
		Short: "Connect to instances via SSH through AWS SSM",
		Long: `Connect to AWS instances using SSH through AWS Systems Manager Session Manager.

This command allows you to establish SSH connections without requiring inbound ports to be open
or public IP addresses to be assigned to the instances.

Examples:
  gossm ssh                               # Interactive instance and user selection
  gossm ssh -i ~/.ssh/mykey.pem           # Use a specific identity file (interactive instance selection)
  gossm ssh -e "-i key.pem ec2-user@i-123" # Directly specify a complete SSH command
`,
		Run: runSSHCommand,
	}
)

// runSSHCommand executes the SSH operation
func runSSHCommand(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	// Get SSH command details and target instance
	sshArgs, targetName, err := getSSHDetailsAndTarget(ctx)
	if err != nil {
		logErrorAndExit(err)
	}

	// Display information about the SSH command
	internal.PrintReady("ssh", credential.awsConfig.Region, targetName)
	color.Cyan("ssh %s", sshArgs)

	// Start an SSH session through SSM
	session, err := startSSHSession(ctx, targetName)
	if err != nil {
		logErrorAndExit(err)
	}

	// Execute the SSH command
	if err := executeSSHCommand(sshArgs, session, targetName); err != nil {
		color.Red("%v", err)
	}

	// Clean up by terminating the session
	if err := terminateSession(ctx, session.SessionId); err != nil {
		logErrorAndExit(err)
	}
}

// getSSHDetailsAndTarget determines the SSH command and target instance
func getSSHDetailsAndTarget(ctx context.Context) (string, string, error) {
	// Get SSH command arguments
	execFlag := strings.TrimSpace(viper.GetString("ssh-exec"))
	identityFlag := strings.TrimSpace(viper.GetString("ssh-identity"))

	// Validate flags - can't use both exec and identity
	if execFlag != "" && identityFlag != "" {
		return "", "", fmt.Errorf("cannot use both --exec and --identity flags (use only one)")
	}

	// Handle interactive mode
	if execFlag == "" {
		return handleInteractiveSSH(ctx, identityFlag)
	}

	// Handle direct command mode
	return handleDirectSSHCommand(ctx, execFlag)
}

// handleInteractiveSSH handles interactive selection of instance and user
func handleInteractiveSSH(ctx context.Context, identityFlag string) (string, string, error) {
	// Ask for target instance
	target, err := internal.AskTarget(ctx, *credential.awsConfig)
	if err != nil {
		return "", "", fmt.Errorf("failed to select target instance: %w", err)
	}

	// Ask for SSH user
	sshUser, err := internal.AskUser()
	if err != nil {
		return "", "", fmt.Errorf("failed to select SSH user: %w", err)
	}

	// Generate SSH command
	sshCommand := internal.GenerateSSHExecCommand("", identityFlag, sshUser.Name, target.PublicDomain)

	return sshCommand, target.Name, nil
}

// handleDirectSSHCommand processes a directly specified SSH command
func handleDirectSSHCommand(ctx context.Context, execFlag string) (string, string, error) {
	// Parse the exec command to extract the server
	parts := strings.Split(execFlag, " ")

	// The server should be the last part of the command (user@server)
	lastPart := parts[len(parts)-1]
	serverParts := strings.Split(lastPart, "@")

	if len(serverParts) < 2 {
		return "", "", fmt.Errorf("invalid SSH command format: must include user@server")
	}

	// Extract server hostname
	server := serverParts[len(serverParts)-1]

	// Resolve server to IP
	ips, err := net.LookupIP(server)
	if err != nil || len(ips) == 0 {
		return "", "", fmt.Errorf("failed to resolve hostname '%s': %w", server, err)
	}

	ip := ips[0].String()

	// Find instance by IP
	instanceID, err := internal.FindInstanceIdByIp(ctx, *credential.awsConfig, ip)
	if err != nil {
		return "", "", fmt.Errorf("failed to find instance by IP '%s': %w", ip, err)
	}

	if instanceID == "" {
		return "", "", fmt.Errorf("no matching instance found for IP '%s'", ip)
	}

	// Generate SSH command
	sshCommand := internal.GenerateSSHExecCommand(execFlag, "", "", "")

	return sshCommand, instanceID, nil
}

// executeSSHCommand executes the SSH command with SSM as proxy
func executeSSHCommand(sshArgs string, session *ssm.StartSessionOutput, targetName string) error {
	// Marshal session information to JSON
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	// Create parameter input for the SSM plugin
	input := &ssm.StartSessionInput{
		DocumentName: aws.String(documentNameSSH),
		Parameters:   map[string][]string{"portNumber": {defaultSSHPort}},
		Target:       aws.String(targetName),
	}

	// Marshal parameters to JSON
	paramsJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal session parameters: %w", err)
	}

	// Build proxy command for SSH
	proxyCommand := fmt.Sprintf("ProxyCommand=%s '%s' %s %s %s '%s'",
		credential.ssmPluginPath,
		string(sessionJSON),
		credential.awsConfig.Region,
		"StartSession",
		credential.awsProfile,
		string(paramsJSON),
	)

	// Build SSH command arguments
	cmdArgs := []string{"-o", proxyCommand}
	for _, arg := range strings.Fields(sshArgs) {
		if arg != "" {
			cmdArgs = append(cmdArgs, arg)
		}
	}

	// Execute SSH command
	return internal.CallProcess("ssh", cmdArgs...)
}

func init() {
	// Define command flags
	sshCommand.Flags().StringP("exec", "e", "", "Complete SSH command (e.g., \"-i key.pem ec2-user@instance\")")
	sshCommand.Flags().StringP("identity", "i", "", "SSH identity file path (e.g., ~/.ssh/id_rsa)")

	// Bind flags to viper
	viper.BindPFlag("ssh-exec", sshCommand.Flags().Lookup("exec"))
	viper.BindPFlag("ssh-identity", sshCommand.Flags().Lookup("identity"))

	// Add command to root
	rootCmd.AddCommand(sshCommand)
}
