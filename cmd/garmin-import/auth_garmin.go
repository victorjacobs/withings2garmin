package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/victorjacobs/garmin-import/internal/garmin"
	"github.com/victorjacobs/garmin-import/internal/secret"
)

func (cli *cli) newGarminAuthCommand() *cobra.Command {
	var emailFile string
	var passwordFile string
	var mfaCodeFile string
	var force bool

	command := &cobra.Command{
		Use:   "garmin",
		Short: "Authorize access to Garmin Connect",
		RunE: func(command *cobra.Command, _ []string) error {
			return cli.runGarminAuth(command, force, emailFile, passwordFile, mfaCodeFile)
		},
	}
	command.Flags().StringVar(&emailFile, "email-file", "", "File containing the Garmin email")
	command.Flags().StringVar(&passwordFile, "password-file", "", "File containing the Garmin password")
	command.Flags().StringVar(&mfaCodeFile, "mfa-code-file", "", "File containing an MFA code")
	command.Flags().BoolVar(&force, "force", false, "Perform full authentication even with a valid saved token")
	markFlagRequired(command, "email-file")
	markFlagRequired(command, "password-file")

	return command
}

func (cli *cli) runGarminAuth(
	command *cobra.Command,
	force bool,
	emailFile, passwordFile, mfaCodeFile string,
) error {
	if !force && cli.savedGarminTokenIsValid(command.Context()) {
		_, err := fmt.Fprintln(command.OutOrStdout(), "Garmin account token is valid")

		return operationalError(err)
	}

	email, err := secret.ReadFile(emailFile, "Garmin email")
	if err != nil {
		return cliError(err)
	}
	password, err := secret.ReadFile(passwordFile, "Garmin password")
	if err != nil {
		return cliError(err)
	}
	mfaCode, err := optionalSecret(mfaCodeFile, "Garmin MFA code")
	if err != nil {
		return cliError(err)
	}

	authenticator, err := garmin.NewAuthenticator(nil, "", "", "")
	if err != nil {
		return operationalError(err)
	}
	token, err := authenticator.Login(command.Context(), garmin.Credentials{
		Email:    string(email),
		Password: string(password),
		MFACode:  mfaCode,
	})
	if err != nil {
		return classifiedError(err)
	}
	if err := cli.runtime.Store.SaveGarminTokens(garminStateToken(token)); err != nil {
		return operationalError(err)
	}

	_, err = fmt.Fprintln(command.OutOrStdout(), "Garmin account token stored")

	return operationalError(err)
}

func (cli *cli) savedGarminTokenIsValid(ctx context.Context) bool {
	tokens, err := cli.runtime.Store.LoadGarminTokens()
	if err != nil || !tokens.ExpiresAt.After(time.Now()) {
		return false
	}

	return cli.runtime.Garmin.Validate(ctx, tokens.AccessToken) == nil
}
