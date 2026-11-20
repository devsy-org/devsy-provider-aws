package cmd

import (
	"errors"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

// NewRootCmd returns a new root command.
func NewRootCmd() *cobra.Command {
	awsCmd := &cobra.Command{
		Use:           "devsy-provider-aws",
		Short:         "aws Provider commands",
		SilenceErrors: true,
		SilenceUsage:  true,

		PersistentPreRunE: func(cobraCmd *cobra.Command, args []string) error {
			cfg, err := loggingConfig(os.Getenv("DEVSY_LOG_LEVEL"), os.Getenv("DEVSY_LOG_FORMAT"))
			if err != nil {
				log.Init(log.Config{DefaultLevel: log.LevelInfoName, Format: "json"})
				return err
			}
			log.Init(cfg)

			return nil
		},
	}

	return awsCmd
}

func loggingConfig(level, format string) (log.Config, error) {
	cfg, invalid := logging.ResolveProvider(level, format)
	if invalid.Level {
		return log.Config{}, errors.New("invalid DEVSY_LOG_LEVEL")
	}
	if invalid.Format {
		return log.Config{}, errors.New("invalid DEVSY_LOG_FORMAT")
	}
	if level != "" {
		level = logging.LevelName(cfg.Level)
	}
	return log.Config{
		Level:        level,
		DefaultLevel: log.LevelInfoName,
		Format:       string(cfg.Format),
	}, nil
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// build the root command
	rootCmd := BuildRoot()

	// execute command
	err := rootCmd.Execute()
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			os.Exit(exitErr.ExitStatus())
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			if len(exitErr.Stderr) > 0 {
				log.Error(string(exitErr.Stderr))
			}

			os.Exit(exitErr.ExitCode())
		}

		log.Fatal(err)
	}
}

// BuildRoot creates a new root command from the.
func BuildRoot() *cobra.Command {
	rootCmd := NewRootCmd()

	rootCmd.AddCommand(NewInitCmd())
	rootCmd.AddCommand(NewCreateCmd())
	rootCmd.AddCommand(NewDeleteCmd())
	rootCmd.AddCommand(NewCommandCmd())
	rootCmd.AddCommand(NewStartCmd())
	rootCmd.AddCommand(NewStopCmd())
	rootCmd.AddCommand(NewStatusCmd())
	rootCmd.AddCommand(NewDescribeCmd())

	return rootCmd
}
