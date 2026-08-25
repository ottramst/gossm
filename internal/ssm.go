package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	"github.com/fatih/color"
)

const (
	// maxOutputResults is the maximum number of results per API call
	maxOutputResults = 50

	// shellDocumentName is the SSM document for running shell commands
	shellDocumentName = "AWS-RunShellScript"

	// commandTimeout is the timeout for SSM commands in seconds
	commandTimeout = 60
)

// pollInterval is the interval for checking command status.
// It is a variable so tests can shorten it.
var pollInterval = 1 * time.Second

// Target represents an AWS EC2 instance target
type Target struct {
	Name          string // AWS Instance ID
	PublicDomain  string // Public DNS Name
	PrivateDomain string // Private DNS Name
}

// User represents an SSH user
type User struct {
	Name string // Username
}

// Region represents an AWS region
type Region struct {
	Name string // Region code (e.g., us-east-1)
}

// Port represents port forwarding configuration
type Port struct {
	Remote string // Remote port
	Local  string // Local port
}

// ec2API is the subset of the EC2 client used by gossm; *ec2.Client implements it.
type ec2API interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

// ssmAPI is the subset of the SSM client used by gossm; *ssm.Client implements it.
type ssmAPI interface {
	DescribeInstanceInformation(ctx context.Context, params *ssm.DescribeInstanceInformationInput, optFns ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
	GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
	TerminateSession(ctx context.Context, params *ssm.TerminateSessionInput, optFns ...func(*ssm.Options)) (*ssm.TerminateSessionOutput, error)
}

// AskUser prompts the user to select an SSH username
func AskUser() (*User, error) {
	prompt := &survey.Input{
		Message: "Type your connect ssh user (default: root):",
	}
	var user string
	if err := survey.AskOne(prompt, &user); err != nil {
		return nil, err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = "root"
	}
	return &User{Name: user}, nil
}

// AskRegion prompts the user to select an AWS region
func AskRegion(ctx context.Context, cfg aws.Config) (*Region, error) {
	// Get regions from AWS API
	regions, err := getAvailableRegions(ctx, ec2.NewFromConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("failed to list AWS regions (ec2:DescribeRegions is required for interactive region selection; pass --region to skip it): %w", err)
	}

	slices.Sort(regions)

	// Prompt user to select a region
	prompt := &survey.Select{
		Message: "Choose a region in AWS:",
		Options: regions,
	}

	var selectedRegion string
	err = survey.AskOne(prompt, &selectedRegion,
		survey.WithIcons(func(icons *survey.IconSet) {
			icons.SelectFocus.Format = "green+hb"
		}),
		survey.WithPageSize(20))

	if err != nil {
		return nil, fmt.Errorf("region selection failed: %w", err)
	}

	return &Region{Name: selectedRegion}, nil
}

// getAvailableRegions fetches available AWS regions
func getAvailableRegions(ctx context.Context, client ec2API) ([]string, error) {
	output, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}

	regions := make([]string, 0, len(output.Regions))
	for _, region := range output.Regions {
		if region.RegionName != nil {
			regions = append(regions, *region.RegionName)
		}
	}

	return regions, nil
}

// AskTarget prompts the user to select a single EC2 instance
func AskTarget(ctx context.Context, cfg aws.Config) (*Target, error) {
	// Get available instances
	instances, err := FindInstances(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Create a list of instance options
	options := make([]string, 0, len(instances))
	for k := range instances {
		options = append(options, k)
	}
	slices.Sort(options)

	if len(options) == 0 {
		return nil, errors.New("no EC2 instances found")
	}

	// Prompt user to select an instance
	prompt := &survey.Select{
		Message: "Choose a target in AWS:",
		Options: options,
	}

	var selectedKey string
	err = survey.AskOne(prompt, &selectedKey,
		survey.WithIcons(func(icons *survey.IconSet) {
			icons.SelectFocus.Format = "green+hb"
		}),
		survey.WithPageSize(20))

	if err != nil {
		return nil, fmt.Errorf("target selection failed: %w", err)
	}

	return instances[selectedKey], nil
}

// AskMultiTarget prompts the user to select multiple EC2 instances
func AskMultiTarget(ctx context.Context, cfg aws.Config) ([]*Target, error) {
	// Get available instances
	instances, err := FindInstances(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Create a list of instance options
	options := make([]string, 0, len(instances))
	for k := range instances {
		options = append(options, k)
	}
	slices.Sort(options)

	if len(options) == 0 {
		return nil, errors.New("no EC2 instances found")
	}

	// Prompt user to select multiple instances
	prompt := &survey.MultiSelect{
		Message: "Choose targets in AWS:",
		Options: options,
	}

	var selectedKeys []string
	if err := survey.AskOne(prompt, &selectedKeys, survey.WithPageSize(20)); err != nil {
		return nil, fmt.Errorf("target selection failed: %w", err)
	}

	// Create list of selected targets
	targets := make([]*Target, 0, len(selectedKeys))
	for _, k := range selectedKeys {
		targets = append(targets, instances[k])
	}

	return targets, nil
}

// AskPorts prompts the user for port forwarding configuration
func AskPorts() (*Port, error) {
	port := &Port{}

	// Prepare prompts for remote and local ports
	prompts := []*survey.Question{
		{
			Name:   "remote",
			Prompt: &survey.Input{Message: "Remote port to access:"},
		},
		{
			Name:   "local",
			Prompt: &survey.Input{Message: "Local port number to forward:"},
		},
	}

	if err := survey.Ask(prompts, port); err != nil {
		return nil, WrapError(err)
	}

	// Validate remote port
	port.Remote = strings.TrimSpace(port.Remote)
	if _, err := strconv.Atoi(port.Remote); err != nil {
		return nil, errors.New("you must specify a valid port number")
	}

	// Use remote port for local port if not specified
	port.Local = strings.TrimSpace(port.Local)
	if port.Local == "" {
		port.Local = port.Remote
	}

	// Validate port numbers
	if len(port.Remote) > 5 || len(port.Local) > 5 {
		return nil, errors.New("you must specify a valid port number")
	}

	return port, nil
}

// FindInstances returns all running EC2 instances that have SSM agent
func FindInstances(ctx context.Context, cfg aws.Config) (map[string]*Target, error) {
	return findInstances(ctx, ec2.NewFromConfig(cfg), ssm.NewFromConfig(cfg))
}

func findInstances(ctx context.Context, client ec2API, ssmClient ssmAPI) (map[string]*Target, error) {
	table := make(map[string]*Target)

	// Find instance IDs with connected SSM agent
	instanceIDs, err := findInstanceIdsWithConnectedSSM(ctx, ssmClient)
	if err != nil {
		return nil, err
	}

	// Process instances in batches (AWS API limit is 200 filters per call)
	for len(instanceIDs) > 0 {
		// AWS caps a single DescribeInstances filter at 200 values
		batchSize := min(len(instanceIDs), 199)

		// Get batch of instances
		batch := instanceIDs[:batchSize]
		instanceIDs = instanceIDs[batchSize:]

		// Describe the instances
		output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []ec2types.Filter{
				{Name: aws.String("instance-state-name"), Values: []string{"running"}},
				{Name: aws.String("instance-id"), Values: batch},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to describe instances: %w", err)
		}

		// Process instance details
		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				// Find instance name from tags
				name := ""
				for _, tag := range instance.Tags {
					if aws.ToString(tag.Key) == "Name" {
						name = aws.ToString(tag.Value)
						break
					}
				}

				// Add to table of instances
				displayName := fmt.Sprintf("%s\t(%s)", name, *instance.InstanceId)
				table[displayName] = &Target{
					Name:          aws.ToString(instance.InstanceId),
					PublicDomain:  aws.ToString(instance.PublicDnsName),
					PrivateDomain: aws.ToString(instance.PrivateDnsName),
				}
			}
		}
	}

	return table, nil
}

// findInstanceIdsWithConnectedSSM returns instance IDs that have SSM agent connected
func findInstanceIdsWithConnectedSSM(ctx context.Context, client ssmAPI) ([]string, error) {
	var instanceIDs []string

	paginator := ssm.NewDescribeInstanceInformationPaginator(client, &ssm.DescribeInstanceInformationInput{
		MaxResults: aws.Int32(maxOutputResults),
	})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to describe instance information: %w", err)
		}
		for _, info := range output.InstanceInformationList {
			if info.InstanceId != nil {
				instanceIDs = append(instanceIDs, *info.InstanceId)
			}
		}
	}

	return instanceIDs, nil
}

// FindInstanceIdByIp finds an EC2 instance ID by IP address
func FindInstanceIdByIp(ctx context.Context, cfg aws.Config, ip string) (string, error) {
	return findInstanceIdByIp(ctx, ec2.NewFromConfig(cfg), ip)
}

func findInstanceIdByIp(ctx context.Context, client ec2API, ip string) (string, error) {
	// Function to find an instance with matching IP
	findInstanceWithIP := func(output *ec2.DescribeInstancesOutput) string {
		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				// Skip instances without IP addresses
				if instance.PublicIpAddress == nil && instance.PrivateIpAddress == nil {
					continue
				}

				// Check if public or private IP matches
				if ip == aws.ToString(instance.PublicIpAddress) ||
					ip == aws.ToString(instance.PrivateIpAddress) {
					return *instance.InstanceId
				}
			}
		}
		return ""
	}

	paginator := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{
		MaxResults: aws.Int32(maxOutputResults),
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to describe instances: %w", err)
		}
		if instanceID := findInstanceWithIP(output); instanceID != "" {
			return instanceID, nil
		}
	}

	return "", fmt.Errorf("no instance found with IP address: %s", ip)
}

// AskHost prompts the user for a host address
func AskHost() (string, error) {
	prompt := &survey.Input{
		Message: "Type your host address you want to forward to:",
	}

	var host string
	if err := survey.AskOne(prompt, &host); err != nil {
		return "", err
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("you must specify a host address")
	}

	return host, nil
}

// CreateStartSession creates an SSM session
func CreateStartSession(ctx context.Context, cfg aws.Config, input *ssm.StartSessionInput) (*ssm.StartSessionOutput, error) {
	client := ssm.NewFromConfig(cfg)
	return client.StartSession(ctx, input)
}

// DeleteStartSession terminates an SSM session
func DeleteStartSession(ctx context.Context, cfg aws.Config, input *ssm.TerminateSessionInput) error {
	return deleteStartSession(ctx, ssm.NewFromConfig(cfg), input)
}

func deleteStartSession(ctx context.Context, client ssmAPI, input *ssm.TerminateSessionInput) error {
	fmt.Printf("%s %s\n",
		color.YellowString("Delete Session"),
		color.YellowString(aws.ToString(input.SessionId)))

	if _, err := client.TerminateSession(ctx, input); err != nil {
		// A session that ended on its own (the plugin exiting normally) is
		// already terminated server-side, and the API rejects terminating
		// it again — that is success, not an error.
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "ValidationException", "DoesNotExistException":
				return nil
			}
		}
		return fmt.Errorf("failed to terminate session: %w", err)
	}

	return nil
}

// SendCommand sends a command to EC2 instances via SSM
func SendCommand(ctx context.Context, cfg aws.Config, targets []*Target, command string) (*ssm.SendCommandOutput, error) {
	client := ssm.NewFromConfig(cfg)
	return client.SendCommand(ctx, buildSendCommandInput(targets, command))
}

// buildSendCommandInput constructs the SSM SendCommand request for running a
// shell command on the given targets.
func buildSendCommandInput(targets []*Target, command string) *ssm.SendCommandInput {
	// Extract instance IDs from targets
	instanceIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		instanceIDs = append(instanceIDs, target.Name)
	}

	return &ssm.SendCommandInput{
		DocumentName:   aws.String(shellDocumentName),
		InstanceIds:    instanceIDs,
		TimeoutSeconds: aws.Int32(commandTimeout),
		CloudWatchOutputConfig: &ssmtypes.CloudWatchOutputConfig{
			CloudWatchOutputEnabled: true,
		},
		Parameters: map[string][]string{
			"commands": {command},
		},
	}
}

// PrintCommandInvocation watches and displays command invocation results
func PrintCommandInvocation(ctx context.Context, cfg aws.Config, inputs []*ssm.GetCommandInvocationInput) {
	client := ssm.NewFromConfig(cfg)
	var wg sync.WaitGroup

	// Process each command invocation in parallel
	for _, input := range inputs {
		wg.Go(func() { monitorCommandInvocation(ctx, client, input) })
	}

	wg.Wait()
}

// monitorCommandInvocation monitors a single command invocation
func monitorCommandInvocation(ctx context.Context, client ssmAPI, input *ssm.GetCommandInvocationInput) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			output, err := client.GetCommandInvocation(ctx, input)
			if err != nil {
				color.Red("Failed to get command invocation: %v", err)
				return
			}

			// Check command status
			status := strings.ToLower(string(output.Status))
			switch status {
			case "pending", "inprogress", "delayed":
				// Still running, continue polling
				continue
			case "success":
				fmt.Printf("[%s][%s] %s\n",
					color.GreenString("success"),
					color.YellowString(*output.InstanceId),
					color.GreenString(*output.StandardOutputContent))
				return
			default:
				fmt.Printf("[%s][%s] %s\n",
					color.RedString("error"),
					color.YellowString(*output.InstanceId),
					color.RedString(*output.StandardErrorContent))
				return
			}
		}
	}
}

// GenerateSSHExecCommand generates an SSH command string
func GenerateSSHExecCommand(exec, identity, user, domain string) string {
	var newExec string

	// Create base command
	if exec == "" {
		newExec = fmt.Sprintf("%s@%s", user, domain)
	} else {
		newExec = exec
	}

	// Check if command already includes identity flag
	hasIdentityFlag := strings.Contains(newExec, " -i ")

	// Add identity flag if needed
	if !hasIdentityFlag && identity != "" {
		newExec = fmt.Sprintf("-i %s %s", identity, newExec)
	}

	return newExec
}

// PrintReady displays information about the command to be run
func PrintReady(cmd, region, target string) {
	fmt.Printf("[%s] region: %s, target: %s\n",
		color.GreenString(cmd),
		color.YellowString(region),
		color.YellowString(target))
}

// CallProcess executes an external process with escape sequence support for interactive sessions
func CallProcess(process string, args ...string) error {
	// Use simple escape sequence handler for interactive sessions
	return CallProcessWithSimpleEscape(process, args...)
}

// CallProcessDirect executes an external process without escape sequence handling
func CallProcessDirect(process string, args ...string) error {
	// Create command
	cmd := exec.Command(process, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin

	// Set up signal handling
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	done := make(chan bool, 1)

	// Handle signals
	go func() {
		for {
			select {
			case <-sigs:
				// Ignore SIGINT, process handles it
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	// Run process
	if err := cmd.Run(); err != nil {
		return WrapError(err)
	}

	return nil
}
