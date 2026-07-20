package main

import "github.com/spf13/cobra"

func (cli *cli) newAuthCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Authenticate Withings or Garmin"}
	command.AddCommand(cli.newWithingsAuthCommand(), cli.newGarminAuthCommand())
	return command
}
