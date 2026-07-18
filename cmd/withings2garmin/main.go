package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/victorjacobs/withings2garmin/internal/app"
	"github.com/victorjacobs/withings2garmin/internal/config"
	"github.com/victorjacobs/withings2garmin/internal/garmin"
	"github.com/victorjacobs/withings2garmin/internal/secret"
	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

var (
	version   = "0.1.0"
	revision  = "dirty"
	buildDate = "unknown"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(arguments []string) int {
	global := flag.NewFlagSet("withings2garmin", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	stateDir := global.String("state-dir", "", "state directory")
	logLevel := global.String("log-level", "info", "debug, info, warn, or error")
	if err := global.Parse(arguments); err != nil {
		return app.ExitCLI
	}
	if global.NArg() == 0 {
		if err := usage(global); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return app.ExitCLI
	}
	level, err := config.ParseLogLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return app.ExitCLI
	}
	directory, err := config.ResolveStateDir(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return app.ExitCLI
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(levelToSlog(level))}))
	runtime, err := app.New(state.NewStore(directory), logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return app.ExitFailure
	}
	ctx := context.Background()
	command := global.Arg(0)
	args := global.Args()[1:]
	var exit int
	switch command {
	case "version":
		fmt.Printf("withings2garmin %s (%s, %s)\n", version, revision, buildDate)
		return app.ExitSuccess
	case "auth":
		exit = auth(ctx, runtime, args)
	case "sync":
		exit = sync(ctx, runtime, args)
	case "status":
		exit = status(ctx, runtime, args)
	default:
		if err := usage(global); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return app.ExitCLI
	}
	return exit
}

func usage(fs *flag.FlagSet) error {
	_, err := fmt.Fprint(fs.Output(), "usage: withings2garmin [--state-dir PATH] [--log-level LEVEL] COMMAND\ncommands: auth, sync, status, version\n")

	return err
}

func auth(ctx context.Context, runtime *app.Runtime, arguments []string) int {
	if len(arguments) == 0 {
		fmt.Fprintln(os.Stderr, "usage: auth withings|garmin")
		return app.ExitCLI
	}
	switch arguments[0] {
	case "withings":
		fs := flag.NewFlagSet("auth withings", flag.ContinueOnError)
		clientID := fs.String("client-id", os.Getenv("WITHINGS_CLIENT_ID"), "Withings client ID")
		secretFile := fs.String("client-secret-file", "", "secret file")
		redirect := fs.String("redirect-uri", "", "redirect URI")
		if err := fs.Parse(arguments[1:]); err != nil {
			return app.ExitCLI
		}
		credentials, err := secret.ReadFile(*secretFile, "Withings client secret")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		oauth := withings.OAuthConfig{ClientID: *clientID, ClientSecret: string(credentials), RedirectURI: *redirect}
		oauthState, err := withings.NewState()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitFailure
		}
		url, err := oauth.AuthorizationURL(oauthState)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		fmt.Printf("Open this URL; the returned code expires in 30 seconds:\n%s\nPaste the full redirect URL: ", url)
		var callback string
		if _, err := fmt.Fscanln(os.Stdin, &callback); err != nil {
			fmt.Fprintln(os.Stderr, "read redirect URL:", err)
			return app.ExitFailure
		}
		code, err := withings.ParseRedirect(callback, oauthState)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		token, err := runtime.Withings.ExchangeCode(ctx, oauth, code)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitFailure
		}
		storedToken := state.WithingsTokens{
			SchemaVersion: 1,
			UserID:        token.UserID,
			AccessToken:   token.AccessToken,
			RefreshToken:  token.RefreshToken,
			Scope:         token.Scope,
			TokenType:     token.TokenType,
			ObtainedAt:    token.ObtainedAt,
			ExpiresAt:     token.ExpiresAt,
		}

		if err := runtime.Store.SaveWithingsTokens(storedToken); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitFailure
		}
		fmt.Printf("Withings authorization stored; expires %s\n", token.ExpiresAt.Format(time.RFC3339))
		return app.ExitSuccess
	case "garmin":
		fs := flag.NewFlagSet("auth garmin", flag.ContinueOnError)
		emailFile := fs.String("email-file", "", "email file")
		passwordFile := fs.String("password-file", "", "password file")
		mfaFile := fs.String("mfa-code-file", "", "MFA file")
		force := fs.Bool("force", false, "force login")
		if err := fs.Parse(arguments[1:]); err != nil {
			return app.ExitCLI
		}
		if !*force {
			if tokens, err := runtime.Store.LoadGarminTokens(); err == nil && !tokens.ExpiresAt.Before(time.Now()) {
				if err := runtime.Garmin.Validate(ctx, tokens.AccessToken); err == nil {
					fmt.Println("Garmin account token is valid")
					return app.ExitSuccess
				}
			}
		}
		email, err := secret.ReadFile(*emailFile, "Garmin email")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		password, err := secret.ReadFile(*passwordFile, "Garmin password")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		var mfa string
		if *mfaFile != "" {
			code, err := secret.ReadFile(*mfaFile, "Garmin MFA code")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return app.ExitCLI
			}
			mfa = string(code)
		}
		authenticator, err := garmin.NewAuthenticator(nil, "", "", "")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitFailure
		}
		credentials := garmin.Credentials{
			Email:    string(email),
			Password: string(password),
			MFACode:  mfa,
		}

		token, err := authenticator.Login(ctx, credentials)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return errorExit(err)
		}
		storedToken := state.GarminTokens{
			SchemaVersion: 1,
			AccessToken:   token.AccessToken,
			RefreshToken:  token.RefreshToken,
			ClientID:      token.ClientID,
			ExpiresAt:     token.ExpiresAt,
			ObtainedAt:    time.Now().UTC(),
		}

		if err := runtime.Store.SaveGarminTokens(storedToken); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitFailure
		}
		fmt.Println("Garmin account token stored")
		return app.ExitSuccess
	default:
		fmt.Fprintln(os.Stderr, "usage: auth withings|garmin")
		return app.ExitCLI
	}
}

func sync(ctx context.Context, runtime *app.Runtime, arguments []string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	clientID := fs.String("client-id", os.Getenv("WITHINGS_CLIENT_ID"), "Withings client ID")
	secretFile := fs.String("client-secret-file", "", "secret file")
	from := fs.String("from", "", "YYYY-MM-DD")
	to := fs.String("to", "", "YYYY-MM-DD")
	lookback := fs.Duration("initial-lookback", 30*24*time.Hour, "first-run lookback")
	ambiguous := fs.Bool("include-ambiguous", false, "include ambiguous readings")
	dry := fs.Bool("dry-run", false, "do not upload")
	max := fs.Int("max-uploads", 0, "maximum uploads")
	if err := fs.Parse(arguments); err != nil {
		return app.ExitCLI
	}
	secretValue, err := secret.ReadFile(*secretFile, "Withings client secret")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return app.ExitCLI
	}
	var start, end *time.Time
	if *from != "" || *to != "" {
		a, err := time.Parse("2006-01-02", *from)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		b, err := time.Parse("2006-01-02", *to)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return app.ExitCLI
		}
		finish := b.Add(24*time.Hour - time.Second)
		start = &a
		end = &finish
		if *max == 0 {
			*max = 100
		}
	}
	oauth := withings.OAuthConfig{
		ClientID:     *clientID,
		ClientSecret: string(secretValue),
		RedirectURI:  "https://unused.invalid/callback",
	}
	options := app.SyncOptions{
		OAuth:            oauth,
		From:             start,
		To:               end,
		InitialLookback:  *lookback,
		IncludeAmbiguous: *ambiguous,
		DryRun:           *dry,
		MaxUploads:       *max,
	}

	result, err := runtime.Sync(ctx, options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return errorExit(err)
	}
	fmt.Printf(
		"fetched=%d uploaded=%d reconciled=%d ignored=%d conflicts=%d would_upload=%d\n",
		result.Fetched,
		result.Uploaded,
		result.Reconciled,
		result.Ignored,
		result.Conflicts,
		result.WouldUpload,
	)
	if result.Conflicts > 0 {
		return app.ExitConflict
	}
	return app.ExitSuccess
}

func status(ctx context.Context, runtime *app.Runtime, arguments []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	check := fs.Bool("check", false, "validate saved Garmin token")
	if err := fs.Parse(arguments); err != nil {
		return app.ExitCLI
	}
	wt, werr := runtime.Store.LoadWithingsTokens()
	gt, gerr := runtime.Store.LoadGarminTokens()
	syncState, serr := runtime.Store.LoadSyncState()
	fmt.Printf("withings_tokens=%t garmin_tokens=%t sync_state=%t\n", werr == nil, gerr == nil, serr == nil)
	if werr == nil {
		fmt.Println("withings_access_expires=", wt.ExpiresAt.Format(time.RFC3339))
	}
	if gerr == nil {
		fmt.Println("garmin_access_expires=", gt.ExpiresAt.Format(time.RFC3339))
	}
	if serr == nil {
		counts := map[state.LedgerState]int{}
		for _, entry := range syncState.Ledger {
			counts[entry.State]++
		}
		fmt.Printf(
			"cursor=%d pending=%d uploaded=%d reconciled=%d ignored=%d conflict=%d\n",
			syncState.WithingsCursor,
			counts[state.LedgerPending],
			counts[state.LedgerUploaded],
			counts[state.LedgerReconciled],
			counts[state.LedgerIgnored],
			counts[state.LedgerConflict],
		)
	}
	if *check {
		if gerr != nil {
			fmt.Fprintln(os.Stderr, gerr)
			return app.ExitReauth
		}
		if err := runtime.Garmin.Validate(ctx, gt.AccessToken); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return errorExit(err)
		}
	}
	return app.ExitSuccess
}

func errorExit(err error) int {
	if errors.Is(err, garmin.ErrAuthenticationRequired) || errors.Is(err, withings.ErrAuthenticationRequired) {
		return app.ExitReauth
	}
	return app.ExitFailure
}
func levelToSlog(level config.LogLevel) slog.Level {
	switch level {
	case config.LogLevelDebug:
		return slog.LevelDebug
	case config.LogLevelWarn:
		return slog.LevelWarn
	case config.LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
