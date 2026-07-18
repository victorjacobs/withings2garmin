package garmin

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUploadWeightRequestPreservesLocalAndUTC(t *testing.T) {
	location, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 7, 18, 6, 12, 34, 0, time.UTC)
	body, err := marshalWeightUpload(instant, location, 75420)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"dateTimestamp":"2026-07-18T08:12:34.000","gmtTimestamp":"2026-07-18T06:12:34.000","unitKey":"kg","sourceType":"MANUAL","value":75.42}` + "\n"
	if string(body) != want {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func TestClientUploadWeightContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/weight-service/user-weight" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("authorization missing")
		}
		if r.Header.Get("X-Garmin-Client-Platform") != "Android" {
			t.Errorf("native headers missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewClient(server.Client(), server.URL, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UploadWeight(context.Background(), "access", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), time.UTC, 70000); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshContract(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Unix()
	token := testJWT("GARMIN_CONNECT_MOBILE_ANDROID_DI", expiresAt)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/di-oauth2-service/oauth/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
			t.Errorf("unexpected refresh form")
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("GARMIN_CONNECT_MOBILE_ANDROID_DI:"))
		if r.Header.Get("Authorization") != wantAuth {
			t.Errorf("basic auth = %q", r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rotated"}`, token)
	}))
	defer server.Close()
	client, err := NewClient(server.Client(), server.URL, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Refresh(context.Background(), TokenSet{ClientID: "GARMIN_CONNECT_MOBILE_ANDROID_DI", RefreshToken: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "rotated" || !got.ExpiresAt.Equal(time.Unix(expiresAt, 0).UTC()) {
		t.Fatalf("unexpected token result: %+v", got)
	}
}

func TestDecodeWeightSamplesVariants(t *testing.T) {
	body := []byte(`{"dailyWeightSummaries":[{"weight": "75.42", "gmtTimestamp":"2026-07-18T06:12:34.000", "samplePk": 123}]}`)
	samples, err := decodeWeightSamples(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].WeightGrams != 75420 || samples[0].SamplePK != "123" {
		t.Fatalf("samples = %+v", samples)
	}
}

func testJWT(clientID string, expiresAt int64) string {
	encode := func(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }
	return strings.Join([]string{encode(`{"alg":"none"}`), encode(fmt.Sprintf(`{"client_id":%q,"exp":%d}`, clientID, expiresAt)), "signature"}, ".")
}
