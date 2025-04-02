package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/fatih/color"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ottramst/gossm/internal"
)

const (
	// defaultProfile is the AWS profile name to use when none is specified
	defaultProfile = "default"
)

var (
	// rootCmd represents the base command when called without any sub-commands
	rootCmd = &cobra.Command{
		Use:   "gossm",
		Short: `gossm is an interactive CLI tool to select and connect to AWS servers using AWS Systems Manager Session Manager.`,
		Long: `gossm is an interactive CLI tool that allows you to select servers in AWS and connect 
or send files to your AWS servers using start-session, ssh, scp via AWS Systems Manager Session Manager.`,
	}

	// credential holds the AWS configuration for the current session
	credential *Credential

	// credentialWithMFA is the path to the file containing temporary credentials obtained via MFA
	credentialWithMFA = fmt.Sprintf("%s_mfa", config.DefaultSharedCredentialsFilename())
)

// Credential holds AWS configuration and credential information for the session
type Credential struct {
	// awsProfile is the AWS profile name being used
	awsProfile string

	// awsConfig contains the AWS SDK configuration including region and credentials
	awsConfig *aws.Config

	// gossmHomePath is the path to the gossm home directory (~/.gossm)
	gossmHomePath string

	// ssmPluginPath is the path to the AWS SSM plugin executable
	ssmPluginPath string
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute(version string) {
	rootCmd.Version = version
	if err := rootCmd.Execute(); err != nil {
		logErrorAndExit(err)
	}
}

// logErrorAndExit prints an error message and exits the program
func logErrorAndExit(err error) {
	fmt.Println(color.RedString("[err] %s", err.Error()))
	os.Exit(1)
}

// initConfig reads in config file and ENV variables if set.
// It initializes the AWS configuration and SSM plugin.
func initConfig() {
	credential = &Credential{}

	// 1. Get AWS profile
	awsProfile := getAWSProfile()
	credential.awsProfile = awsProfile

	// 2. Get region from command line or environment
	awsRegion := viper.GetString("region")

	// 3. Setup gossm home directory and SSM plugin
	setupGossmHomeAndPlugin()

	// 4. Setup AWS credentials using the AWS SDK's credential chain
	setupAWSCredentials(awsProfile, awsRegion)

	// 5. Ensure region is set, prompt user if needed
	if credential.awsConfig.Region == "" {
		askRegion, err := internal.AskRegion(context.Background(), *credential.awsConfig)
		if err != nil {
			logErrorAndExit(internal.WrapError(err))
		}
		credential.awsConfig.Region = askRegion.Name
	}

	color.Green("AWS region: %s", credential.awsConfig.Region)
}

// getAWSProfile determines the AWS profile to use
func getAWSProfile() string {
	profileFromFlag := viper.GetString("profile")
	if profileFromFlag != "" {
		return profileFromFlag
	}

	profileFromEnv := os.Getenv("AWS_PROFILE")
	if profileFromEnv != "" {
		return profileFromEnv
	}

	return defaultProfile
}

// setupGossmHomeAndPlugin sets up the gossm home directory and SSM plugin
func setupGossmHomeAndPlugin() {
	home, err := homedir.Dir()
	if err != nil {
		logErrorAndExit(internal.WrapError(err))
	}

	credential.gossmHomePath = filepath.Join(home, ".gossm")
	if err := os.MkdirAll(credential.gossmHomePath, os.ModePerm); err != nil && !os.IsExist(err) {
		logErrorAndExit(internal.WrapError(err))
	}

	plugin, err := internal.GetSsmPlugin()
	if err != nil {
		logErrorAndExit(internal.WrapError(err))
	}

	credential.ssmPluginPath = filepath.Join(credential.gossmHomePath, internal.GetSsmPluginName())
	setupSsmPlugin(plugin)
}

// setupSsmPlugin installs or updates the SSM plugin if needed
func setupSsmPlugin(plugin []byte) {
	info, err := os.Stat(credential.ssmPluginPath)

	if os.IsNotExist(err) {
		color.Green("[create] aws ssm plugin")
		if err := os.WriteFile(credential.ssmPluginPath, plugin, 0755); err != nil {
			logErrorAndExit(internal.WrapError(err))
		}
		return
	}

	if err != nil {
		logErrorAndExit(internal.WrapError(err))
	}

	if int(info.Size()) != len(plugin) {
		color.Green("[update] aws ssm plugin")
		if err := os.WriteFile(credential.ssmPluginPath, plugin, 0755); err != nil {
			logErrorAndExit(internal.WrapError(err))
		}
	}
}

// setupAWSCredentials sets up AWS credentials using the AWS SDK's credential chain
func setupAWSCredentials(awsProfile, awsRegion string) {
	// Check if we need special handling for MFA subcommand
	args := os.Args[1:]
	subcmd, _, err := rootCmd.Find(args)
	if err != nil {
		logErrorAndExit(internal.WrapError(err))
	}

	// Check for special MFA credentials file
	if _, err := os.Stat(credentialWithMFA); err == nil && os.Getenv("AWS_SHAREDcredentialS_FILE") == "" {
		color.Yellow("[Use] gossm default mfa credential file %s", credentialWithMFA)
		os.Setenv("AWS_SHAREDcredentialS_FILE", credentialWithMFA)
	}

	// For MFA command, ensure we're using credentials without session tokens
	if subcmd.Use == "mfa" {
		// Here we could clear any session tokens or use a separate profile that doesn't use session tokens
		// This depends on the specifics of how the MFA command is implemented
	}

	// Use AWS SDK's built-in credential chain with our profile
	configOpts := []func(*config.LoadOptions) error{
		config.WithSharedConfigProfile(credential.awsProfile),
	}

	// Add region if specified
	if awsRegion != "" {
		configOpts = append(configOpts, config.WithRegion(awsRegion))
	}

	// Load AWS configuration
	awsConfig, err := config.LoadDefaultConfig(context.Background(), configOpts...)
	if err != nil {
		logErrorAndExit(internal.WrapError(fmt.Errorf("failed to load AWS configuration: %w", err)))
	}

	// Verify credentials are valid
	creds, err := awsConfig.Credentials.Retrieve(context.Background())
	if err != nil {
		logErrorAndExit(internal.WrapError(fmt.Errorf("failed to retrieve AWS credentials: %w", err)))
	}

	// Validate credentials
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		logErrorAndExit(internal.WrapError(fmt.Errorf("invalid AWS credentials: missing access key or secret key")))
	}

	credential.awsConfig = &awsConfig
}

// init sets up the command flags and initializes the configuration system
func init() {
	cobra.OnInitialize(initConfig)

	// Define persistent flags for the root command
	rootCmd.PersistentFlags().StringP("profile", "p", "",
		`AWS profile name (default is AWS_PROFILE environment variable or "default")`)
	rootCmd.PersistentFlags().StringP("region", "r", "",
		`AWS region to use for operations`)

	// Initialize default version flag
	rootCmd.InitDefaultVersionFlag()

	// Bind flags to viper for configuration
	viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile"))
	viper.BindPFlag("region", rootCmd.PersistentFlags().Lookup("region"))
}
