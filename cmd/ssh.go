package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"

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

Escape Sequence:
  Enter ~.   Disconnect (ssh's built-in escape; useful when network is stuck)

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

	flagExec, _ := cmd.Flags().GetString("exec")
	flagIdentity, _ := cmd.Flags().GetString("identity")

	// Get SSH command details and target instance
	sshArgs, targetName, err := getSSHDetailsAndTarget(ctx, flagExec, flagIdentity)
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
	if err := runProxiedCommand("ssh", sshArgs, session, sshSessionInput(targetName)); err != nil {
		color.Red("%v", err)
	}

	// Clean up by terminating the session
	if err := terminateSession(ctx, session.SessionId); err != nil {
		logErrorAndExit(err)
	}
}

// getSSHDetailsAndTarget determines the SSH command and target instance
func getSSHDetailsAndTarget(ctx context.Context, flagExec, flagIdentity string) (string, string, error) {
	// Get SSH command arguments
	execFlag := strings.TrimSpace(flagExec)
	identityFlag := strings.TrimSpace(flagIdentity)

	// Validate flags - can't use both exec and identity
	if execFlag != "" && identityFlag != "" {
		return "", "", errors.New("cannot use both --exec and --identity flags (use only one)")
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
	sshCommand := internal.GenerateSSHExecCommand("", identityFlag, sshUser.Name, sshHostForTarget(target))

	return sshCommand, target.Name, nil
}

// sshHostForTarget picks the host to place on the SSH command line: the
// public DNS name when present, otherwise the instance ID — the SSM
// ProxyCommand tunnels by instance ID either way.
func sshHostForTarget(target *internal.Target) string {
	if target.PublicDomain != "" {
		return target.PublicDomain
	}
	return target.Name
}

// handleDirectSSHCommand processes a directly specified SSH command
func handleDirectSSHCommand(ctx context.Context, execFlag string) (string, string, error) {
	parts, err := shellquote.Split(execFlag)
	if err != nil {
		return "", "", fmt.Errorf("invalid SSH command: %w", err)
	}

	host, err := hostFromSSHArgs(parts)
	if err != nil {
		return "", "", err
	}

	instanceID, err := resolveTargetHost(ctx, host)
	if err != nil {
		return "", "", err
	}

	// Generate SSH command
	sshCommand := internal.GenerateSSHExecCommand(execFlag, "", "", "")

	return sshCommand, instanceID, nil
}

// hostFromSSHArgs extracts the host from parsed SSH arguments; the last
// argument must be user@host.
func hostFromSSHArgs(parts []string) (string, error) {
	if len(parts) == 0 {
		return "", errors.New("invalid SSH command format: must include user@server")
	}
	lastPart := parts[len(parts)-1]
	serverParts := strings.Split(lastPart, "@")
	if len(serverParts) < 2 || serverParts[len(serverParts)-1] == "" {
		return "", errors.New("invalid SSH command format: must include user@server")
	}
	return serverParts[len(serverParts)-1], nil
}

// resolveTargetHost resolves an ssh/scp destination host to an EC2 instance
// ID: instance IDs are used directly, anything else is resolved via DNS and
// matched against running instances by IP.
func resolveTargetHost(ctx context.Context, host string) (string, error) {
	if strings.HasPrefix(host, "i-") || strings.HasPrefix(host, "mi-") {
		return host, nil
	}

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("failed to resolve hostname '%s': %w", host, err)
	}
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

// runProxiedCommand executes ssh or scp with an SSM ProxyCommand option
// followed by the user-supplied arguments. It is shared by ssh and scp.
func runProxiedCommand(process, userArgs string, session *ssm.StartSessionOutput, input *ssm.StartSessionInput) error {
	// Marshal session and parameters to JSON for the SSM plugin
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}
	paramsJSON, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal session parameters: %w", err)
	}

	proxyCommand := fmt.Sprintf("ProxyCommand=%s '%s' %s %s %s '%s'",
		credential.ssmPluginPath,
		string(sessionJSON),
		credential.awsConfig.Region,
		"StartSession",
		credential.awsProfile,
		string(paramsJSON),
	)

	// Build the command arguments, respecting quoted segments
	parsedArgs, err := shellquote.Split(userArgs)
	if err != nil {
		return fmt.Errorf("invalid %s arguments: %w", process, err)
	}

	// Run ssh/scp with the terminal inherited directly: they manage their
	// own tty (host-key prompts read it, ssh has its native ~. escape), and
	// the raw-mode escape wrapper would steal their input and break output
	// line endings.
	return internal.CallProcessDirect(process, append([]string{"-o", proxyCommand}, parsedArgs...)...)
}

func init() {
	// Define command flags
	sshCommand.Flags().StringP("exec", "e", "", "Complete SSH command (e.g., \"-i key.pem ec2-user@instance\")")
	sshCommand.Flags().StringP("identity", "i", "", "SSH identity file path (e.g., ~/.ssh/id_rsa)")

	// Add command to root
	rootCmd.AddCommand(sshCommand)
}
