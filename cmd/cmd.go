package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ottramst/gossm/internal"
)

const (
	// commandWaitTime is the duration to wait for command execution results
	commandWaitTime = 3 * time.Second
)

var (
	// cmdCommand is the Cobra command for executing AWS Systems Manager Run Command
	cmdCommand = &cobra.Command{
		Use:   "cmd",
		Short: "Execute SSM Run Command on AWS instances",
		Long:  "Execute AWS Systems Manager Run Command on selected instances with an interactive CLI",
		Run:   runCommand,
	}
)

// findSpecificTarget looks for a specific target by name
func findSpecificTarget(ctx context.Context, targetName string) ([]*internal.Target, error) {
	// Get all available instances
	allInstances, err := internal.FindInstances(ctx, *credential.awsConfig)
	if err != nil {
		return nil, err
	}

	// Find the specified target
	for _, instance := range allInstances {
		if instance.Name == targetName {
			return []*internal.Target{instance}, nil
		}
	}

	// If we get here, the specified target wasn't found
	return nil, fmt.Errorf("target instance '%s' not found", targetName)
}

// findTargetInstances identifies the instances to target for command execution
func findTargetInstances(ctx context.Context) ([]*internal.Target, error) {
	// Check if a specific target was specified
	argTarget := strings.TrimSpace(viper.GetString("cmd-target"))
	if argTarget != "" {
		return findSpecificTarget(ctx, argTarget)
	}

	// If no specific target, prompt user to select targets
	return internal.AskMultiTarget(ctx, *credential.awsConfig)
}

// displayCommandInfo shows information about the command to be executed
func displayCommandInfo(execCommand string, targets []*internal.Target) {
	// Build a string of target names
	var targetNames strings.Builder
	for i, target := range targets {
		if i > 0 {
			targetNames.WriteString(", ")
		}
		targetNames.WriteString(target.Name)
	}

	// Display command information
	internal.PrintReady(execCommand, credential.awsConfig.Region, targetNames.String())
}

// displayCommandResults waits for and displays the results of command execution
func displayCommandResults(ctx context.Context, sendOutput *ssm.SendCommandOutput) {
	fmt.Printf("%s\n", color.YellowString("Waiting for command results..."))

	// Wait for command execution to complete
	time.Sleep(commandWaitTime)

	// Create inputs for getting command results
	var invocationInputs []*ssm.GetCommandInvocationInput
	for _, instanceID := range sendOutput.Command.InstanceIds {
		invocationInputs = append(invocationInputs, &ssm.GetCommandInvocationInput{
			CommandId:  sendOutput.Command.CommandId,
			InstanceId: aws.String(instanceID),
		})
	}

	// Display command results
	internal.PrintCommandInvocation(ctx, *credential.awsConfig, invocationInputs)
}

// runCommand executes the SSM Run Command operation
func runCommand(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	// Get the command to execute
	execCommand := strings.TrimSpace(viper.GetString("cmd-exec"))
	if execCommand == "" {
		logErrorAndExit(fmt.Errorf("command execution failed: no command specified"))
	}

	// Find target instances
	targets, err := findTargetInstances(ctx)
	if err != nil {
		logErrorAndExit(err)
	}

	// Display command information
	displayCommandInfo(execCommand, targets)

	// Send the command to the targets
	sendOutput, err := internal.SendCommand(ctx, *credential.awsConfig, targets, execCommand)
	if err != nil {
		logErrorAndExit(err)
	}

	// Wait for and display command results
	displayCommandResults(ctx, sendOutput)
}

func init() {
	// Define command flags
	cmdCommand.Flags().StringP("exec", "e", "", "Command to execute on the target instances (required)")
	cmdCommand.Flags().StringP("target", "t", "", "Target EC2 instance name (optional, will prompt if not specified)")

	// Mark required flags
	cmdCommand.MarkFlagRequired("exec")

	// Bind flags to viper
	viper.BindPFlag("cmd-exec", cmdCommand.Flags().Lookup("exec"))
	viper.BindPFlag("cmd-target", cmdCommand.Flags().Lookup("target"))

	// Add command to root
	rootCmd.AddCommand(cmdCommand)
}
