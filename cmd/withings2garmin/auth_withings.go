package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/victorjacobs/withings2garmin/internal/secret"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

func (cli *cli) newWithingsAuthCommand() *cobra.Command {
	var clientID string
	var secretFile string
	var redirectURI string

	command := &cobra.Command{
		Use:   "withings",
		Short: "Authorize access to Withings",
		RunE: func(command *cobra.Command, _ []string) error {
			return cli.runWithingsAuth(command, clientID, secretFile, redirectURI)
		},
	}
	command.Flags().StringVar(&clientID, "client-id", os.Getenv("WITHINGS_CLIENT_ID"), "Withings client ID")
	command.Flags().StringVar(&secretFile, "client-secret-file", "", "File containing the Withings client secret")
	command.Flags().StringVar(&redirectURI, "redirect-uri", "", "OAuth redirect URI")
	markFlagRequired(command, "client-id")
	markFlagRequired(command, "client-secret-file")
	markFlagRequired(command, "redirect-uri")

	return command
}

func (cli *cli) runWithingsAuth(command *cobra.Command, clientID, secretFile, redirectURI string) error {
	credentials, err := secret.ReadFile(secretFile, "Withings client secret")
	if err != nil {
		return cliError(err)
	}
	oauth := withings.OAuthConfig{
		ClientID:     clientID,
		ClientSecret: string(credentials),
		RedirectURI:  redirectURI,
	}
	stateValue, err := withings.NewState()
	if err != nil {
		return operationalError(err)
	}
	authorizationURL, err := oauth.AuthorizationURL(stateValue)
	if err != nil {
		return cliError(err)
	}
	if _, err := fmt.Fprintf(
		command.OutOrStdout(),
		"Open this URL; the returned code expires in 30 seconds:\n%s\nPaste the full redirect URL: ",
		authorizationURL,
	); err != nil {
		return operationalError(err)
	}

	var callback string
	if _, err := fmt.Fscanln(command.InOrStdin(), &callback); err != nil {
		return operationalError(err)
	}
	code, err := withings.ParseRedirect(callback, stateValue)
	if err != nil {
		return cliError(err)
	}
	token, err := cli.runtime.Withings.ExchangeCode(command.Context(), oauth, code)
	if err != nil {
		return operationalError(err)
	}
	if err := cli.runtime.Store.SaveWithingsTokens(withingsStateToken(token)); err != nil {
		return operationalError(err)
	}

	_, err = fmt.Fprintf(
		command.OutOrStdout(),
		"Withings authorization stored; expires %s\n",
		token.ExpiresAt.Format(time.RFC3339),
	)

	return operationalError(err)
}
