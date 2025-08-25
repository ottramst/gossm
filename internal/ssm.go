package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
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
	"github.com/fatih/color"
)

const (
	// maxOutputResults is the maximum number of results per API call
	maxOutputResults = 50

	// shellDocumentName is the SSM document for running shell commands
	shellDocumentName = "AWS-RunShellScript"

	// commandTimeout is the timeout for SSM commands in seconds
	commandTimeout = 60

	// pollInterval is the interval for checking command status
	pollInterval = 1 * time.Second
)

// AWS region list - kept for fallback if API fails
var defaultAwsRegions = []string{
	"af-south-1",
	"ap-east-1", "ap-northeast-1", "ap-northeast-2", "ap-northeast-3", "ap-south-1", "ap-southeast-2", "ap-southeast-3",
	"ca-central-1",
	"cn-north-1", "cn-northwest-1",
	"eu-central-1", "eu-north-1", "eu-south-1", "eu-west-1", "eu-west-2", "eu-west-3",
	"me-south-1",
	"sa-east-1",
	"us-east-1", "us-east-2", "us-gov-east-1", "us-gov-west-2", "us-west-1", "us-west-2",
}

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

// AskUser prompts the user to select an SSH username
func AskUser() (*User, error) {
	prompt := &survey.Input{
		Message: "Type your connect ssh user (default: root):",
	}
	var user string
	survey.AskOne(prompt, &user)
	user = strings.TrimSpace(user)
	if user == "" {
		user = "root"
	}
	return &User{Name: user}, nil
}

// AskRegion prompts the user to select an AWS region
func AskRegion(ctx context.Context, cfg aws.Config) (*Region, error) {
	// Get regions from AWS API
	regions, err := getAvailableRegions(ctx, cfg)
	if err != nil {
		// Fall back to default regions if API call fails
		regions = make([]string, len(defaultAwsRegions))
		copy(regions, defaultAwsRegions)
	}

	sort.Strings(regions)

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
func getAvailableRegions(ctx context.Context, cfg aws.Config) ([]string, error) {
	client := ec2.NewFromConfig(cfg)

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
	sort.Strings(options)

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
	sort.Strings(options)

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
	client := ec2.NewFromConfig(cfg)
	table := make(map[string]*Target)

	// Find instance IDs with connected SSM agent
	instanceIDs, err := FindInstanceIdsWithConnectedSSM(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Process instances in batches (AWS API limit is 200 filters per call)
	for len(instanceIDs) > 0 {
		batchSize := len(instanceIDs)
		if batchSize >= 200 {
			batchSize = 199
		}

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

// FindInstanceIdsWithConnectedSSM returns instance IDs that have SSM agent connected
func FindInstanceIdsWithConnectedSSM(ctx context.Context, cfg aws.Config) ([]string, error) {
	client := ssm.NewFromConfig(cfg)
	instanceIDs := []string{}

	// Initial query for instances with SSM
	output, err := client.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		MaxResults: aws.Int32(maxOutputResults),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe instance information: %w", err)
	}

	// Process first page of results
	for _, info := range output.InstanceInformationList {
		if info.InstanceId != nil {
			instanceIDs = append(instanceIDs, *info.InstanceId)
		}
	}

	// Process any additional pages of results
	nextToken := output.NextToken
	for nextToken != nil && *nextToken != "" {
		nextOutput, err := client.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(maxOutputResults),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to describe additional instance information: %w", err)
		}

		for _, info := range nextOutput.InstanceInformationList {
			if info.InstanceId != nil {
				instanceIDs = append(instanceIDs, *info.InstanceId)
			}
		}

		nextToken = nextOutput.NextToken
	}

	return instanceIDs, nil
}

// FindInstanceIdByIp finds an EC2 instance ID by IP address
func FindInstanceIdByIp(ctx context.Context, cfg aws.Config, ip string) (string, error) {
	client := ec2.NewFromConfig(cfg)

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

	// Initial query for running instances
	output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		MaxResults: aws.Int32(maxOutputResults),
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe instances: %w", err)
	}

	// Check first page of results
	instanceID := findInstanceWithIP(output)
	if instanceID != "" {
		return instanceID, nil
	}

	// Process any additional pages of results
	nextToken := output.NextToken
	for nextToken != nil && *nextToken != "" {
		nextOutput, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			MaxResults: aws.Int32(maxOutputResults),
			NextToken:  nextToken,
			Filters: []ec2types.Filter{
				{Name: aws.String("instance-state-name"), Values: []string{"running"}},
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to describe additional instances: %w", err)
		}

		instanceID = findInstanceWithIP(nextOutput)
		if instanceID != "" {
			return instanceID, nil
		}

		nextToken = nextOutput.NextToken
	}

	return "", fmt.Errorf("no instance found with IP address: %s", ip)
}

// FindDomainByInstanceId finds DNS names for an EC2 instance by ID
func FindDomainByInstanceId(ctx context.Context, cfg aws.Config, instanceID string) ([]string, error) {
	client := ec2.NewFromConfig(cfg)

	// Function to find domain names for an instance
	findDomainForInstance := func(output *ec2.DescribeInstancesOutput, id string) []string {
		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				if aws.ToString(instance.InstanceId) == id {
					return []string{
						aws.ToString(instance.PublicDnsName),
						aws.ToString(instance.PrivateDnsName),
					}
				}
			}
		}
		return []string{}
	}

	// Initial query for running instances
	output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		MaxResults: aws.Int32(maxOutputResults),
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return []string{}, fmt.Errorf("failed to describe instances: %w", err)
	}

	// Check first page of results
	domain := findDomainForInstance(output, instanceID)
	if len(domain) != 0 {
		return domain, nil
	}

	// Process any additional pages of results
	nextToken := output.NextToken
	for nextToken != nil && *nextToken != "" {
		nextOutput, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			MaxResults: aws.Int32(maxOutputResults),
			NextToken:  nextToken,
			Filters: []ec2types.Filter{
				{Name: aws.String("instance-state-name"), Values: []string{"running"}},
			},
		})
		if err != nil {
			return []string{}, fmt.Errorf("failed to describe additional instances: %w", err)
		}

		domain = findDomainForInstance(nextOutput, instanceID)
		if len(domain) != 0 {
			return domain, nil
		}

		nextToken = nextOutput.NextToken
	}

	return []string{}, fmt.Errorf("no domains found for instance: %s", instanceID)
}

// AskHost prompts the user for a host address
func AskHost() (string, error) {
	prompt := &survey.Input{
		Message: "Type your host address you want to forward to:",
	}

	var host string
	survey.AskOne(prompt, &host)

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
	client := ssm.NewFromConfig(cfg)

	fmt.Printf("%s %s\n",
		color.YellowString("Delete Session"),
		color.YellowString(aws.ToString(input.SessionId)))

	_, err := client.TerminateSession(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to terminate session: %w", err)
	}

	return nil
}

// SendCommand sends a command to EC2 instances via SSM
func SendCommand(ctx context.Context, cfg aws.Config, targets []*Target, command string) (*ssm.SendCommandOutput, error) {
	client := ssm.NewFromConfig(cfg)

	// Extract instance IDs from targets
	instanceIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		instanceIDs = append(instanceIDs, target.Name)
	}

	// Create command input
	input := &ssm.SendCommandInput{
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

	return client.SendCommand(ctx, input)
}

// PrintCommandInvocation watches and displays command invocation results
func PrintCommandInvocation(ctx context.Context, cfg aws.Config, inputs []*ssm.GetCommandInvocationInput) {
	client := ssm.NewFromConfig(cfg)
	wg := &sync.WaitGroup{}

	// Process each command invocation in parallel
	for _, input := range inputs {
		wg.Add(1)
		go monitorCommandInvocation(ctx, client, input, wg)
	}

	wg.Wait()
}

// monitorCommandInvocation monitors a single command invocation
func monitorCommandInvocation(ctx context.Context, client *ssm.Client, input *ssm.GetCommandInvocationInput, wg *sync.WaitGroup) {
	defer wg.Done()

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
