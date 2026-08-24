package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/ottramst/gossm/internal"
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

// findTargetInstances identifies the instances to target for command
// execution, using the given flag value or prompting when it is empty
func findTargetInstances(ctx context.Context, flagTarget string) ([]*internal.Target, error) {
	argTarget := strings.TrimSpace(flagTarget)
	if argTarget != "" {
		target, err := findSpecificInstance(ctx, argTarget)
		if err != nil {
			return nil, err
		}
		return []*internal.Target{target}, nil
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

	// Create inputs for getting command results
	invocationInputs := make([]*ssm.GetCommandInvocationInput, 0, len(sendOutput.Command.InstanceIds))
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

	flagExec, _ := cmd.Flags().GetString("exec")
	flagTarget, _ := cmd.Flags().GetString("target")

	// Get the command to execute
	execCommand := strings.TrimSpace(flagExec)
	if execCommand == "" {
		logErrorAndExit(errors.New("command execution failed: no command specified"))
	}

	// Find target instances
	targets, err := findTargetInstances(ctx, flagTarget)
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
	cmdCommand.Flags().StringP("target", "t", "", "Target EC2 instance ID (optional, will prompt if not specified)")

	// Mark required flags
	_ = cmdCommand.MarkFlagRequired("exec")

	// Add command to root
	rootCmd.AddCommand(cmdCommand)
}
