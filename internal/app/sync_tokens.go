package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/victorjacobs/garmin-import/internal/state"
	"github.com/victorjacobs/garmin-import/internal/withings"
)

const (
	withingsRefreshWindow = 5 * time.Minute
	garminRefreshWindow   = 15 * time.Minute
)

func (runtime *Runtime) prepareTokens(
	ctx context.Context,
	oauth withings.OAuthConfig,
	withingsTokens state.WithingsTokens,
	garminTokens state.GarminTokens,
) (state.WithingsTokens, state.GarminTokens, error) {
	var err error

	withingsTokens, err = runtime.refreshWithingsIfNeeded(ctx, oauth, withingsTokens)
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, err
	}

	garminTokens, err = runtime.refreshGarminIfNeeded(ctx, garminTokens)
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, err
	}

	if err := runtime.Garmin.Validate(ctx, garminTokens.AccessToken); err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, fmt.Errorf(
			"validate Garmin token: %w",
			err,
		)
	}

	return withingsTokens, garminTokens, nil
}

func (runtime *Runtime) refreshWithingsIfNeeded(
	ctx context.Context,
	oauth withings.OAuthConfig,
	tokens state.WithingsTokens,
) (state.WithingsTokens, error) {
	if tokens.ExpiresAt.After(runtime.Now().Add(withingsRefreshWindow)) {
		return tokens, nil
	}

	refreshed, err := runtime.Withings.RefreshToken(ctx, oauth, tokens.RefreshToken)
	if err != nil {
		return state.WithingsTokens{}, fmt.Errorf("refresh Withings token: %w", err)
	}

	tokens = withingsStateToken(refreshed)
	if err := runtime.Store.SaveWithingsTokens(tokens); err != nil {
		return state.WithingsTokens{}, err
	}

	return tokens, nil
}

func (runtime *Runtime) refreshGarminIfNeeded(
	ctx context.Context,
	tokens state.GarminTokens,
) (state.GarminTokens, error) {
	if tokens.ExpiresAt.After(runtime.Now().Add(garminRefreshWindow)) {
		return tokens, nil
	}

	refreshed, err := runtime.Garmin.Refresh(ctx, garminToken(tokens))
	if err != nil {
		return state.GarminTokens{}, fmt.Errorf("refresh Garmin token: %w", err)
	}

	tokens = garminStateToken(refreshed)
	if err := runtime.Store.SaveGarminTokens(tokens); err != nil {
		return state.GarminTokens{}, err
	}

	return tokens, nil
}

func (runtime *Runtime) fetchMeasurements(
	ctx context.Context,
	oauth withings.OAuthConfig,
	tokens state.WithingsTokens,
	query withings.Query,
) (withings.FetchResult, error) {
	fetched, err := runtime.Withings.FetchMeasurements(ctx, tokens.AccessToken, query)
	if !errors.Is(err, withings.ErrAuthenticationRequired) {
		return fetched, err
	}

	refreshed, refreshErr := runtime.Withings.RefreshToken(ctx, oauth, tokens.RefreshToken)
	if refreshErr != nil {
		return withings.FetchResult{}, fmt.Errorf(
			"refresh Withings token after authorization failure: %w",
			refreshErr,
		)
	}

	tokens = withingsStateToken(refreshed)
	if err := runtime.Store.SaveWithingsTokens(tokens); err != nil {
		return withings.FetchResult{}, err
	}

	return runtime.Withings.FetchMeasurements(ctx, tokens.AccessToken, query)
}
