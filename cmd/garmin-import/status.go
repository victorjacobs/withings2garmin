package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/victorjacobs/garmin-import/internal/app"
	"github.com/victorjacobs/garmin-import/internal/state"
)

func (cli *cli) newStatusCommand() *cobra.Command {
	var check bool

	command := &cobra.Command{
		Use:   "status",
		Short: "Report local token and synchronization state",
		RunE: func(command *cobra.Command, _ []string) error {
			return cli.runStatus(command, check)
		},
	}
	command.Flags().BoolVar(&check, "check", false, "Validate the saved Garmin token without writing")

	return command
}

func (cli *cli) runStatus(command *cobra.Command, check bool) error {
	withingsTokens, withingsErr := cli.runtime.Store.LoadWithingsTokens()
	garminTokens, garminErr := cli.runtime.Store.LoadGarminTokens()
	syncState, syncErr := cli.runtime.Store.LoadSyncState()

	if err := writeStatus(command, withingsTokens, withingsErr, garminTokens, garminErr, syncState, syncErr); err != nil {
		return operationalError(err)
	}
	if !check {
		return nil
	}
	if garminErr != nil {
		return &commandError{exitCode: app.ExitReauth, err: garminErr}
	}
	if err := cli.runtime.Garmin.Validate(command.Context(), garminTokens.AccessToken); err != nil {
		return classifiedError(err)
	}

	return nil
}

func writeStatus(
	command *cobra.Command,
	withingsTokens state.WithingsTokens,
	withingsErr error,
	garminTokens state.GarminTokens,
	garminErr error,
	syncState state.SyncState,
	syncErr error,
) error {
	if _, err := fmt.Fprintf(
		command.OutOrStdout(),
		"withings_tokens=%t garmin_tokens=%t sync_state=%t\n",
		withingsErr == nil,
		garminErr == nil,
		syncErr == nil,
	); err != nil {
		return err
	}
	if withingsErr == nil {
		if _, err := fmt.Fprintf(
			command.OutOrStdout(),
			"withings_access_expires=%s\n",
			withingsTokens.ExpiresAt.Format(time.RFC3339),
		); err != nil {
			return err
		}
	}
	if garminErr == nil {
		if _, err := fmt.Fprintf(
			command.OutOrStdout(),
			"garmin_access_expires=%s\n",
			garminTokens.ExpiresAt.Format(time.RFC3339),
		); err != nil {
			return err
		}
	}
	if syncErr == nil {
		return writeLedgerCounts(command, syncState)
	}

	return nil
}

func writeLedgerCounts(command *cobra.Command, syncState state.SyncState) error {
	counts := map[state.LedgerState]int{}
	for _, entry := range syncState.Ledger {
		counts[entry.State]++
	}
	_, err := fmt.Fprintf(
		command.OutOrStdout(),
		"cursor=%d pending=%d uploaded=%d reconciled=%d ignored=%d conflict=%d\n",
		syncState.WithingsCursor,
		counts[state.LedgerPending],
		counts[state.LedgerUploaded],
		counts[state.LedgerReconciled],
		counts[state.LedgerIgnored],
		counts[state.LedgerConflict],
	)

	return err
}
