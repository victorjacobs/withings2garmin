package withings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestAuthorizationURLAndRedirect(t *testing.T) {
	config := OAuthConfig{ClientID: "client id", ClientSecret: "secret", RedirectURI: "https://example.test/callback"}
	authorizeURL, err := config.AuthorizationURL("state value")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("scope") != "user.metrics" || parsed.Query().Get("state") != "state value" {
		t.Fatalf("unexpected query: %s", parsed.RawQuery)
	}
	code, err := ParseRedirect("https://example.test/callback?code=short-lived&state=state+value", "state value")
	if err != nil || code != "short-lived" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if _, err := ParseRedirect("https://example.test/callback?code=x&state=wrong", "state value"); err == nil {
		t.Fatal("expected state mismatch")
	}
}

func TestExchangeAndRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if request.Form.Get("action") != "requesttoken" {
			t.Fatal("missing action")
		}
		if request.Form.Get("grant_type") == "authorization_code" && request.Form.Get("code") != "code" {
			t.Fatal("missing code")
		}
		if request.Form.Get("grant_type") == "refresh_token" && request.Form.Get("refresh_token") != "refresh" {
			t.Fatal("missing refresh")
		}
		userID := `"u"`
		if request.Form.Get("grant_type") == "refresh_token" {
			userID = "123456"
		}
		_, _ = response.Write([]byte(`{"status":0,"body":{` +
			`"userid":` + userID + `,"access_token":"access","refresh_token":"refresh-next",` +
			`"scope":"user.metrics","token_type":"Bearer","expires_in":10800}}`))
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.now = func() time.Time { return time.Unix(100, 0) }
	config := OAuthConfig{
		ClientID:     "id",
		ClientSecret: "secret",
		RedirectURI:  "https://example.test/callback",
		TokenURL:     server.URL,
	}
	token, err := client.ExchangeCode(context.Background(), config, "code")
	if err != nil {
		t.Fatal(err)
	}
	if token.ExpiresAt != time.Unix(10900, 0).UTC() {
		t.Fatalf("expiry %v", token.ExpiresAt)
	}
	refreshed, err := client.RefreshToken(context.Background(), config, "refresh")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.UserID != "123456" {
		t.Fatalf("user ID %q", refreshed.UserID)
	}
}
