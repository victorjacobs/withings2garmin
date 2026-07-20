package main

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/victorjacobs/garmin-import/internal/app"
	"github.com/victorjacobs/garmin-import/internal/config"
	"github.com/victorjacobs/garmin-import/internal/state"
)

type cli struct {
	stateDir string
	logLevel string
	runtime  *app.Runtime
}

type commandError struct {
	exitCode int
	err      error
}

func (err *commandError) Error() string { return err.err.Error() }
func (err *commandError) Unwrap() error { return err.err }

func execute(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		var commandErr *commandError
		if errors.As(err, &commandErr) {
			_, _ = fmt.Fprintln(root.ErrOrStderr(), commandErr.err)

			return commandErr.exitCode
		}

		_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
		return app.ExitFailure
	}

	return app.ExitSuccess
}

func newRootCommand() *cobra.Command {
	cli := &cli{logLevel: "info"}
	root := &cobra.Command{
		Use:               "garmin-import",
		Short:             "Synchronize Withings scale weights to Garmin Connect",
		SilenceErrors:     true,
		SilenceUsage:      true,
		Version:           fmt.Sprintf("%s (%s, %s)", version, revision, buildDate),
		PersistentPreRunE: cli.initialize,
	}
	root.PersistentFlags().StringVar(&cli.stateDir, "state-dir", "", "State directory")
	root.PersistentFlags().StringVar(&cli.logLevel, "log-level", "info", "Log level: debug, info, warn, or error")
	root.AddCommand(cli.newAuthCommand(), cli.newSyncCommand(), cli.newStatusCommand())

	return root
}

func (cli *cli) initialize(command *cobra.Command, _ []string) error {
	level, err := config.ParseLogLevel(cli.logLevel)
	if err != nil {
		return cliError(err)
	}

	directory, err := config.ResolveStateDir(cli.stateDir)
	if err != nil {
		return cliError(err)
	}

	logger := slog.New(slog.NewTextHandler(command.ErrOrStderr(), &slog.HandlerOptions{Level: levelToSlog(level)}))
	cli.runtime, err = app.New(state.NewStore(directory), logger)

	return operationalError(err)
}
