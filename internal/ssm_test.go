package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeEC2 implements ec2API with configurable responses.
type fakeEC2 struct {
	describeInstances func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
	describeRegions   func(*ec2.DescribeRegionsInput) (*ec2.DescribeRegionsOutput, error)
}

func (f *fakeEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return f.describeInstances(in)
}

func (f *fakeEC2) DescribeRegions(_ context.Context, in *ec2.DescribeRegionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	return f.describeRegions(in)
}

// fakeSSM implements ssmAPI with configurable responses.
type fakeSSM struct {
	describeInstanceInformation func(*ssm.DescribeInstanceInformationInput) (*ssm.DescribeInstanceInformationOutput, error)
	getCommandInvocation        func(*ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error)
}

func (f *fakeSSM) DescribeInstanceInformation(_ context.Context, in *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return f.describeInstanceInformation(in)
}

func (f *fakeSSM) GetCommandInvocation(_ context.Context, in *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return f.getCommandInvocation(in)
}

func instanceInfo(ids ...string) []ssmtypes.InstanceInformation {
	infos := make([]ssmtypes.InstanceInformation, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, ssmtypes.InstanceInformation{InstanceId: aws.String(id)})
	}
	return infos
}

func TestFindInstanceIdsWithConnectedSSMPagination(t *testing.T) {
	f := &fakeSSM{describeInstanceInformation: func(in *ssm.DescribeInstanceInformationInput) (*ssm.DescribeInstanceInformationOutput, error) {
		if in.NextToken == nil {
			return &ssm.DescribeInstanceInformationOutput{
				InstanceInformationList: instanceInfo("i-1", "i-2"),
				NextToken:               aws.String("page2"),
			}, nil
		}
		if *in.NextToken != "page2" {
			t.Errorf("unexpected NextToken %q", *in.NextToken)
		}
		return &ssm.DescribeInstanceInformationOutput{
			InstanceInformationList: instanceInfo("i-3"),
		}, nil
	}}

	ids, err := findInstanceIdsWithConnectedSSM(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"i-1", "i-2", "i-3"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestFindInstanceIdsWithConnectedSSMError(t *testing.T) {
	f := &fakeSSM{describeInstanceInformation: func(*ssm.DescribeInstanceInformationInput) (*ssm.DescribeInstanceInformationOutput, error) {
		return nil, errors.New("api down")
	}}

	if _, err := findInstanceIdsWithConnectedSSM(context.Background(), f); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindInstances(t *testing.T) {
	ssmFake := &fakeSSM{describeInstanceInformation: func(*ssm.DescribeInstanceInformationInput) (*ssm.DescribeInstanceInformationOutput, error) {
		return &ssm.DescribeInstanceInformationOutput{InstanceInformationList: instanceInfo("i-1", "i-2")}, nil
	}}

	ec2Fake := &fakeEC2{describeInstances: func(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
		return &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{{
				Instances: []ec2types.Instance{
					{
						InstanceId:     aws.String("i-1"),
						PublicDnsName:  aws.String("pub.example.com"),
						PrivateDnsName: aws.String("priv.example.com"),
						Tags:           []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("web")}},
					},
					{
						InstanceId: aws.String("i-2"),
					},
				},
			}},
		}, nil
	}}

	table, err := findInstances(context.Background(), ec2Fake, ssmFake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	web, ok := table["web\t(i-1)"]
	if !ok {
		t.Fatalf("expected key %q in table, got keys %v", "web\t(i-1)", keys(table))
	}
	if web.Name != "i-1" || web.PublicDomain != "pub.example.com" || web.PrivateDomain != "priv.example.com" {
		t.Errorf("unexpected target %+v", web)
	}

	if _, ok := table["\t(i-2)"]; !ok {
		t.Errorf("expected nameless instance key %q, got keys %v", "\t(i-2)", keys(table))
	}
}

func TestFindInstancesBatching(t *testing.T) {
	// 250 connected instances must be described in batches of at most 199.
	manyIDs := make([]string, 250)
	for i := range manyIDs {
		manyIDs[i] = fmt.Sprintf("i-%03d", i)
	}

	ssmFake := &fakeSSM{describeInstanceInformation: func(*ssm.DescribeInstanceInformationInput) (*ssm.DescribeInstanceInformationOutput, error) {
		return &ssm.DescribeInstanceInformationOutput{InstanceInformationList: instanceInfo(manyIDs...)}, nil
	}}

	var batchSizes []int
	ec2Fake := &fakeEC2{describeInstances: func(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
		for _, f := range in.Filters {
			if aws.ToString(f.Name) == "instance-id" {
				batchSizes = append(batchSizes, len(f.Values))
			}
		}
		return &ec2.DescribeInstancesOutput{}, nil
	}}

	if _, err := findInstances(context.Background(), ec2Fake, ssmFake); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fmt.Sprint(batchSizes) != fmt.Sprint([]int{199, 51}) {
		t.Errorf("batch sizes = %v, want [199 51]", batchSizes)
	}
}

func TestFindInstanceIdByIp(t *testing.T) {
	pageOne := &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:      aws.String("i-pub"),
				PublicIpAddress: aws.String("1.2.3.4"),
			}},
		}},
		NextToken: aws.String("page2"),
	}
	pageTwo := &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:       aws.String("i-priv"),
				PrivateIpAddress: aws.String("10.0.0.5"),
			}},
		}},
	}

	f := &fakeEC2{describeInstances: func(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
		if in.NextToken == nil {
			return pageOne, nil
		}
		return pageTwo, nil
	}}

	tests := []struct {
		ip      string
		want    string
		wantErr bool
	}{
		{"1.2.3.4", "i-pub", false},
		{"10.0.0.5", "i-priv", false},
		{"192.168.0.9", "", true},
	}

	for _, tt := range tests {
		got, err := findInstanceIdByIp(context.Background(), f, tt.ip)
		if tt.wantErr {
			if err == nil {
				t.Errorf("findInstanceIdByIp(%q): expected error, got %q", tt.ip, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("findInstanceIdByIp(%q): unexpected error: %v", tt.ip, err)
			continue
		}
		if got != tt.want {
			t.Errorf("findInstanceIdByIp(%q) = %q, want %q", tt.ip, got, tt.want)
		}
	}
}

func TestGetAvailableRegions(t *testing.T) {
	f := &fakeEC2{describeRegions: func(*ec2.DescribeRegionsInput) (*ec2.DescribeRegionsOutput, error) {
		return &ec2.DescribeRegionsOutput{Regions: []ec2types.Region{
			{RegionName: aws.String("eu-north-1")},
			{RegionName: nil},
			{RegionName: aws.String("us-east-1")},
		}}, nil
	}}

	regions, err := getAvailableRegions(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fmt.Sprint(regions) != fmt.Sprint([]string{"eu-north-1", "us-east-1"}) {
		t.Errorf("regions = %v", regions)
	}
}

func TestBuildSendCommandInput(t *testing.T) {
	targets := []*Target{{Name: "i-1"}, {Name: "i-2"}}
	input := buildSendCommandInput(targets, "uptime")

	if aws.ToString(input.DocumentName) != shellDocumentName {
		t.Errorf("DocumentName = %q", aws.ToString(input.DocumentName))
	}
	if fmt.Sprint(input.InstanceIds) != fmt.Sprint([]string{"i-1", "i-2"}) {
		t.Errorf("InstanceIds = %v", input.InstanceIds)
	}
	if cmds := input.Parameters["commands"]; len(cmds) != 1 || cmds[0] != "uptime" {
		t.Errorf("Parameters[commands] = %v", input.Parameters["commands"])
	}
	if !input.CloudWatchOutputConfig.CloudWatchOutputEnabled {
		t.Error("CloudWatchOutputEnabled = false, want true")
	}
}

func TestMonitorCommandInvocation(t *testing.T) {
	oldInterval := pollInterval
	pollInterval = 5 * time.Millisecond
	defer func() { pollInterval = oldInterval }()

	t.Run("polls until success", func(t *testing.T) {
		calls := 0
		f := &fakeSSM{getCommandInvocation: func(*ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			calls++
			if calls == 1 {
				return &ssm.GetCommandInvocationOutput{
					Status:     ssmtypes.CommandInvocationStatusInProgress,
					InstanceId: aws.String("i-1"),
				}, nil
			}
			return &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				InstanceId:            aws.String("i-1"),
				StandardOutputContent: aws.String("ok"),
			}, nil
		}}

		wg := &sync.WaitGroup{}
		wg.Add(1)
		monitorCommandInvocation(context.Background(), f, &ssm.GetCommandInvocationInput{}, wg)
		wg.Wait()

		if calls < 2 {
			t.Errorf("expected at least 2 polls, got %d", calls)
		}
	})

	t.Run("stops on failed status", func(t *testing.T) {
		f := &fakeSSM{getCommandInvocation: func(*ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:               ssmtypes.CommandInvocationStatusFailed,
				InstanceId:           aws.String("i-1"),
				StandardErrorContent: aws.String("boom"),
			}, nil
		}}

		wg := &sync.WaitGroup{}
		wg.Add(1)
		monitorCommandInvocation(context.Background(), f, &ssm.GetCommandInvocationInput{}, wg)
		wg.Wait()
	})

	t.Run("stops on API error", func(t *testing.T) {
		f := &fakeSSM{getCommandInvocation: func(*ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			return nil, errors.New("api down")
		}}

		wg := &sync.WaitGroup{}
		wg.Add(1)
		monitorCommandInvocation(context.Background(), f, &ssm.GetCommandInvocationInput{}, wg)
		wg.Wait()
	})
}

func TestGenerateSSHExecCommand(t *testing.T) {
	tests := []struct {
		name     string
		exec     string
		identity string
		user     string
		domain   string
		want     string
	}{
		{"default command", "", "", "ec2-user", "host.example.com", "ec2-user@host.example.com"},
		{"explicit command", "ls -la remote:", "", "u", "d", "ls -la remote:"},
		{"identity added", "", "~/.ssh/key.pem", "root", "host", "-i ~/.ssh/key.pem root@host"},
		{"identity already present", "ssh -i /k u@h", "/other", "u", "h", "ssh -i /k u@h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSSHExecCommand(tt.exec, tt.identity, tt.user, tt.domain)
			if got != tt.want {
				t.Errorf("GenerateSSHExecCommand(%q, %q, %q, %q) = %q, want %q",
					tt.exec, tt.identity, tt.user, tt.domain, got, tt.want)
			}
		})
	}
}

func keys(m map[string]*Target) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, strings.ReplaceAll(k, "\t", "\\t"))
	}
	return out
}
