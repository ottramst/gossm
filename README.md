# gossm

`gossm` is an interactive CLI tool that lets you select servers in AWS and connect to them or transfer files using start-session, ssh, or scp through AWS Systems Manager Session Manager.

<p align="center">
<img src="https://storage.googleapis.com/gjbae1212-asset/gossm/start.gif" width="500", height="450" />
</p>

<p align="center"/>
<a href="https://circleci.com/gh/gjbae1212/gossm"><img src="https://circleci.com/gh/gjbae1212/gossm.svg?style=svg"></a>
<a href="https://hits.seeyoufarm.com"/><img src="https://hits.seeyoufarm.com/api/count/incr/badge.svg?url=https%3A%2F%2Fgithub.com%2Fgjbae1212%2Fgossm"/></a>
<a href="/LICENSE"><img src="https://img.shields.io/badge/license-MIT-GREEN.svg" alt="license" /></a>
<a href="https://goreportcard.com/report/github.com/gjbae1212/gossm"><img src="https://goreportcard.com/badge/github.com/gjbae1212/gossm" alt="Go Report Card"/></a>
</p>

## Overview
`gossm` is an interactive CLI tool that integrates with AWS Systems Manager Session Manager.
It helps you select EC2 instances with the AWS SSM agent installed and connect to them using start-session or ssh.
You can also transfer files using scp.

With `gossm`, there's no need to open inbound port 22 on your EC2 instances for SSH or SCP access.
AWS Systems Manager Session Manager uses SSH protocol tunneling for secure communication.

### Additional Features

* `mfa` command to authenticate through AWS MFA and save temporary credentials in $HOME/.aws/credentials_mfa (default expiration: 6 hours)
* `fwd` command for local port forwarding to remote services
* `fwdrem` command for forwarding to a secondary host through an SSM-connected instance
* `cmd` command to execute shell commands on multiple instances at once
   
## Prerequisites

### EC2 Requirements
- EC2 instances must have the [AWS SSM agent](https://docs.aws.amazon.com/systems-manager/latest/userguide/ssm-agent.html) installed 
- Instances need the **AmazonSSMManagedInstanceCore** IAM policy attached
- For ssh/scp functionality, AWS SSM agent version **2.3.672.0 or later** is required

### User Requirements
- Configured AWS credentials
- IAM permissions for:
  - `ec2:DescribeInstances`
  - `ssm:StartSession`
  - `ssm:TerminateSession`
  - `ssm:DescribeSessions`
  - `ssm:DescribeInstanceInformation`
  - `ssm:DescribeInstanceProperties`
  - `ssm:GetConnectionStatus`
- **Recommended**: Permission for `ec2:DescribeRegions` for region selection

## Installation

### Homebrew

Homebrew is not supported yet.

### Download Binary

Download the latest release from the [releases page](https://github.com/ottramst/gossm/releases).

## Usage

### Global Command Arguments

| Argument      | Description              | Default                                |
|---------------|--------------------------|----------------------------------------|
| -p, --profile | AWS profile name to use  | `default` or `$AWS_PROFILE`            |
| -r, --region  | AWS region to connect to | Interactive selection if not specified |

If no profile is specified, gossm will first check for the `AWS_PROFILE` environment variable and then fall back to the `default` profile.

If no region is specified, you can select one through the interactive CLI.

### Commands

#### `start`

Start an interactive terminal session with an EC2 instance.

```bash
$ gossm start
$ gossm start -t i-1234567890abcdef0  # Connect to a specific instance
```

#### `ssh`

Connect to an instance via SSH through AWS SSM.

```bash
# Interactive instance and user selection
$ gossm ssh

# Using a specific identity file
$ gossm ssh -i ~/.ssh/key.pem

# Direct SSH command
$ gossm ssh -e "ec2-user@i-1234567890abcdef0"
$ gossm ssh -e "-i key.pem ec2-user@i-1234567890abcdef0"
```

<p align="center">
<img src="https://storage.googleapis.com/gjbae1212-asset/gossm/ssh.gif" width="500", height="450" />
</p>

#### `scp`

Transfer files to/from instances via SCP through AWS SSM.

```bash
# Transfer a local file to the remote server
$ gossm scp -e "localfile.txt ec2-user@i-1234567890abcdef0:/home/ec2-user/"

# Transfer a remote file to local machine
$ gossm scp -e "-i key.pem ec2-user@i-1234567890abcdef0:/remote/path/file.txt local.txt"
```

#### `cmd`

Execute commands on one or more instances simultaneously.

```bash
# Run a command on interactively selected instances
$ gossm cmd -e "uptime"

# Run a command on a specific instance
$ gossm cmd -e "ls -la" -t i-1234567890abcdef0
```

#### `fwd`
Forward a local port to a port on the remote EC2 instance.

```bash
# Interactive selection
$ gossm fwd

# With specific ports
$ gossm fwd -z 8080 -l 9090  # Remote port 8080 -> Local port 9090
$ gossm fwd -z 8080          # Remote port 8080 -> Local port 8080
```

#### `fwdrem`
Forward a local port to a secondary remote host through an EC2 instance.

```bash
# Forward local port to a remote host through an EC2 instance
$ gossm fwdrem -z 5432 -l 5432 -a internal-db.example.com
```

#### `mfa`
Authenticate with MFA and save temporary credentials for use with AWS CLI and other tools.

```bash
# Authenticate with MFA code
$ gossm mfa 123456

# Set custom expiration time (in seconds)
$ gossm mfa -d 43200 123456  # 12 hours

# For AWS CLI to use these credentials, set in your shell profile:
export AWS_SHARED_CREDENTIALS_FILE=$HOME/.aws/credentials_mfa
```

<p align="center">
<img src="https://storage.googleapis.com/gjbae1212-asset/gossm/mfa.png" />
</p>

## Plugin System

`gossm` automatically manages the AWS Session Manager plugin for you:

- By default, it will download the latest version of the plugin on first use
- You can specify a specific plugin version by setting the `GOSSM_PLUGIN_VERSION` environment variable
- If download fails, it will use the embedded plugin as a fallback

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
