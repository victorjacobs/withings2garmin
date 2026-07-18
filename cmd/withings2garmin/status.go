package main

import "github.com/spf13/cobra"

func (cli *cli) newStatusCommand() *cobra.Command {
	var check bool
	command := &cobra.Command{Use: "status", Short: "Report local token and synchronization state", RunE: func(command *cobra.Command, _ []string) error {
		_, werr := cli.runtime.Store.LoadWithingsTokens()
		gt, gerr := cli.runtime.Store.LoadGarminTokens()
		_, serr := cli.runtime.Store.LoadSyncState()
		if err := writeStatus(command, werr, gerr, serr); err != nil {
			return operationalError(err)
		}
		if check && gerr != nil {
			return &commandError{exitCode: 3, err: gerr}
		}
		if check {
			if err := cli.runtime.Garmin.Validate(command.Context(), gt.AccessToken); err != nil {
				return classifiedError(err)
			}
		}
		return nil
	}}
	command.Flags().BoolVar(&check, "check", false, "Validate the saved Garmin token without writing")
	return command
}
