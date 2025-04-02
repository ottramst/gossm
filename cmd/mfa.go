package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// virtualMFADevice is the ARN format for virtual MFA devices
	virtualMFADevice = "arn:aws:iam::%s:mfa/%s"

	// mfaCredentialFormat is the format for writing AWS credentials to a file
	mfaCredentialFormat = "[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\n"

	// defaultMFADuration is the default duration for MFA credentials in seconds (6 hours)
	defaultMFADuration = 21600

	// mfaTimeout is the maximum time allowed for MFA operations
	mfaTimeout = 60 * time.Second
)

var (
	// mfaCommand is the Cobra command for setting up MFA credentials
	mfaCommand = &cobra.Command{
		Use:   "mfa [token-code]",
		Short: "Authenticate with MFA and save temporary credentials",
		Long: `Authenticate with AWS Multi-Factor Authentication (MFA) and save the temporary 
credentials to the file ~/.aws/credentials_mfa.

You can export AWS_SHARED_CREDENTIALS_FILE environment variable to point to this file
for convenient use with AWS CLI and other tools that use AWS SDK.

Example:
  gossm mfa 123456     # Authenticate with MFA code 123456
`,
		Args: cobra.ExactArgs(1),
		Run:  runMFAAuthentication,
	}
)

// runMFAAuthentication executes the MFA authentication process
func runMFAAuthentication(cmd *cobra.Command, args []string) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), mfaTimeout)
	defer cancel()

	// Get and validate the MFA code
	code := strings.TrimSpace(args[0])
	if code == "" {
		logErrorAndExit(fmt.Errorf("invalid MFA code: code cannot be empty"))
	}

	// Get MFA device identifier
	device, err := getMFADevice(ctx)
	if err != nil {
		logErrorAndExit(err)
	}

	// Get session duration
	duration := viper.GetInt32("mfa-deadline")

	// Get temporary credentials using MFA
	sessionToken, err := getTemporaryCredentials(ctx, device, code, duration)
	if err != nil {
		logErrorAndExit(err)
	}

	// Save credentials to file
	if err := saveTemporaryCredentials(sessionToken); err != nil {
		logErrorAndExit(err)
	}

	// Display success message and instructions
	displayMFASuccessMessage(sessionToken.Credentials.Expiration)
}

// getMFADevice returns the MFA device ARN to use
func getMFADevice(ctx context.Context) (string, error) {
	// Check if device was specified via command line
	device := viper.GetString("mfa-device")
	if device != "" {
		return device, nil
	}

	// If not specified, get the user's virtual MFA device
	client := sts.NewFromConfig(*credential.awsConfig)
	identity, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get identity: %w", err)
	}

	// Extract username from ARN
	arnParts := strings.Split(*identity.Arn, "/")
	if len(arnParts) < 2 {
		return "", fmt.Errorf("unexpected ARN format: %s", *identity.Arn)
	}
	username := arnParts[len(arnParts)-1]

	return fmt.Sprintf(virtualMFADevice, aws.ToString(identity.Account), username), nil
}

// getTemporaryCredentials gets temporary credentials using the MFA token
func getTemporaryCredentials(ctx context.Context, device, code string, duration int32) (*sts.GetSessionTokenOutput, error) {
	client := sts.NewFromConfig(*credential.awsConfig)

	output, err := client.GetSessionToken(ctx, &sts.GetSessionTokenInput{
		DurationSeconds: aws.Int32(duration),
		SerialNumber:    aws.String(device),
		TokenCode:       aws.String(code),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get session token: %w", err)
	}

	return output, nil
}

// saveTemporaryCredentials saves the temporary credentials to a file
func saveTemporaryCredentials(sessionToken *sts.GetSessionTokenOutput) error {
	// Format credentials for file
	formattedCredentials := fmt.Sprintf(
		mfaCredentialFormat,
		defaultProfile,
		*sessionToken.Credentials.AccessKeyId,
		*sessionToken.Credentials.SecretAccessKey,
		*sessionToken.Credentials.SessionToken,
	)

	// Write to file
	if err := os.WriteFile(credentialWithMFA, []byte(formattedCredentials), 0600); err != nil {
		return fmt.Errorf("failed to write credentials to file: %w", err)
	}

	return nil
}

// displayMFASuccessMessage shows a success message and usage instructions
func displayMFASuccessMessage(expiration *time.Time) {
	color.Green("[SUCCESS] Temporary MFA credentials created at %s (expires: %s)",
		credentialWithMFA, expiration.UTC().Format(time.RFC3339))

	fmt.Printf("%s %s %s\n",
		color.YellowString("To use AWS CLI with these credentials, run:"),
		color.CyanString("export AWS_SHARED_CREDENTIALS_FILE=%s", credentialWithMFA),
		color.YellowString("or add this to your shell profile."),
	)
}

func init() {
	// Define command flags
	mfaCommand.Flags().Int32P("deadline", "d", defaultMFADuration,
		"Duration in seconds for the temporary credentials (default: 6 hours)")
	mfaCommand.Flags().StringP("device", "m", "",
		"MFA device ARN (default: your virtual MFA device)")

	// Bind flags to viper
	viper.BindPFlag("mfa-deadline", mfaCommand.Flags().Lookup("deadline"))
	viper.BindPFlag("mfa-device", mfaCommand.Flags().Lookup("device"))

	// Add command to root
	rootCmd.AddCommand(mfaCommand)
}
