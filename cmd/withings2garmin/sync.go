package main

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/victorjacobs/withings2garmin/internal/app"
	"github.com/victorjacobs/withings2garmin/internal/secret"
	"github.com/victorjacobs/withings2garmin/internal/withings"
	"os"
	"time"
)

func (cli *cli) newSyncCommand() *cobra.Command {
	var clientID, secretFile, from, to string
	var lookback time.Duration
	var ambiguous, dry bool
	var max int
	command := &cobra.Command{Use: "sync", Short: "Synchronize pending Withings measurements", RunE: func(command *cobra.Command, _ []string) error {
		credentials, err := secret.ReadFile(secretFile, "Withings client secret")
		if err != nil {
			return cliError(err)
		}
		start, end, err := parseBackfillRange(from, to)
		if err != nil {
			return cliError(err)
		}
		if start != nil && max == 0 {
			max = 100
		}
		options := app.SyncOptions{OAuth: withings.OAuthConfig{ClientID: clientID, ClientSecret: string(credentials), RedirectURI: "https://unused.invalid/callback"}, From: start, To: end, InitialLookback: lookback, IncludeAmbiguous: ambiguous, DryRun: dry, MaxUploads: max}
		result, err := cli.runtime.Sync(command.Context(), options)
		if err != nil {
			return classifiedError(err)
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "fetched=%d uploaded=%d reconciled=%d ignored=%d conflicts=%d would_upload=%d\n", result.Fetched, result.Uploaded, result.Reconciled, result.Ignored, result.Conflicts, result.WouldUpload)
		if err != nil {
			return operationalError(err)
		}
		if result.Conflicts > 0 {
			return &commandError{exitCode: app.ExitConflict, err: errors.New("sync recorded conflicts")}
		}
		return nil
	}}
	command.Flags().StringVar(&clientID, "client-id", os.Getenv("WITHINGS_CLIENT_ID"), "Withings client ID")
	command.Flags().StringVar(&secretFile, "client-secret-file", "", "File containing the Withings client secret")
	command.Flags().StringVar(&from, "from", "", "Backfill start date (YYYY-MM-DD)")
	command.Flags().StringVar(&to, "to", "", "Backfill end date (YYYY-MM-DD)")
	command.Flags().DurationVar(&lookback, "initial-lookback", 30*24*time.Hour, "Initial synchronization lookback")
	command.Flags().BoolVar(&ambiguous, "include-ambiguous", false, "Include ambiguous Withings measurements")
	command.Flags().BoolVar(&dry, "dry-run", false, "Read and reconcile without uploading")
	command.Flags().IntVar(&max, "max-uploads", 0, "Maximum number of uploads")
	markFlagRequired(command, "client-id")
	markFlagRequired(command, "client-secret-file")
	return command
}
