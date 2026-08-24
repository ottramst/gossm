# gossm

`gossm` is an interactive CLI tool that lets you select servers in AWS and connect to them or transfer files using start-session, ssh, or scp through AWS Systems Manager Session Manager.

<p align="center">
<a href="https://github.com/ottramst/gossm/actions/workflows/ci.yml"><img src="https://github.com/ottramst/gossm/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
<a href="https://github.com/ottramst/gossm/releases/latest"><img src="https://img.shields.io/github/v/release/ottramst/gossm" alt="Latest release" /></a>
<a href="https://goreportcard.com/report/github.com/ottramst/gossm"><img src="https://goreportcard.com/badge/github.com/ottramst/gossm" alt="Go Report Card" /></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green.svg" alt="License: MIT" /></a>
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
- For interactive region selection (when no `-r` flag is given): `ec2:DescribeRegions`

## Installation

### Homebrew (macOS)

```sh
brew install --cask ottramst/tap/gossm
```

The cask is updated automatically on every release. Homebrew casks are
macOS-only; on Linux use the [container image](#container) or a binary release.

### Download Binary

Download the latest release from the [releases page](https://github.com/ottramst/gossm/releases).

### Container

Multi-arch images (linux/amd64, linux/arm64) are published to GHCR on every release:

```sh
docker run --rm -it -v ~/.aws:/root/.aws ghcr.io/ottramst/gossm:latest start
```

AWS credentials must be provided to the container: mount `~/.aws` as shown, or pass
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_REGION` with `-e`. The `-it` flags are
required for the interactive prompts and the session terminal.

### Go

```sh
go install github.com/ottramst/gossm@latest
```

## Usage

### Global Command Arguments

| Argument      | Description              | Default                                |
|---------------|--------------------------|----------------------------------------|
| -p, --profile | AWS profile name to use  | `default` or `$AWS_PROFILE`            |
| -r, --region  | AWS region to connect to | Interactive selection if not specified |

If no profile is specified, gossm will first check for the `AWS_PROFILE` environment variable and then fall back to the `default` profile.

If no region is specified, you can select one through the interactive CLI.

### Commands

#### Escape Sequence

When in an interactive session (start, ssh, or scp), you can use the following escape sequence:

- **Enter** followed by `~.` - Disconnect from the session (useful when network connection is stuck)

This works the same way as the standard SSH escape sequence and provides a way to terminate sessions when network connectivity is lost. The tilde character (`~`) is only special when typed immediately after pressing Enter. Using `~` anywhere else (like `~/` for home directory or `~username`) works normally.

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

# Specify the MFA device serial explicitly
$ gossm mfa -m arn:aws:iam::123456789012:mfa/my-device 123456

# For AWS CLI to use these credentials, set in your shell profile:
export AWS_SHARED_CREDENTIALS_FILE=$HOME/.aws/credentials_mfa
```

## Plugin System

`gossm` automatically manages the AWS Session Manager plugin for you:

- By default, it will download the latest version of the plugin on first use
- You can specify a specific plugin version by setting the `GOSSM_PLUGIN_VERSION` environment variable
- If download fails, it will use the embedded plugin as a fallback

## Contributing

Pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
PR-title convention, local development commands, and the release process.

## Acknowledgements

gossm is a maintained fork of [gjbae1212/gossm](https://github.com/gjbae1212/gossm),
whose author designed and built the original tool. This fork continues development
with modernized tooling, automated releases, and new distribution channels.

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
The original work is copyright gjbae1212; modifications in this fork are copyright Ott Ramst.
