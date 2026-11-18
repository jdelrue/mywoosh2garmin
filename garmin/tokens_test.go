package garmin_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mywhoosh2garmin/garmin"
)

func TestSaveLoadTokens(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested")
	oauth1 := &garmin.OAuth1Token{OAuthToken: "t1", OAuthTokenSecret: "s1", Domain: "garmin.cn"}
	oauth2 := &garmin.OAuth2Token{AccessToken: "a2", RefreshToken: "r2", ExpiresAt: 123}
	if err := garmin.SaveTokens(dir, oauth1, oauth2); err != nil {
		t.Fatal(err)
	}

	got1, got2, err := garmin.LoadTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	if *got1 != *oauth1 {
		t.Errorf("oauth1: got %+v, want %+v", got1, oauth1)
	}
	if *got2 != *oauth2 {
		t.Errorf("oauth2: got %+v, want %+v", got2, oauth2)
	}
}

func TestSaveTokensKeepsOAuth1(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oauth1 := &garmin.OAuth1Token{OAuthToken: "t1"}
	if err := garmin.SaveTokens(dir, oauth1, &garmin.OAuth2Token{AccessToken: "old"}); err != nil {
		t.Fatal(err)
	}
	// A refresh saves only the new OAuth2 token.
	if err := garmin.SaveTokens(dir, nil, &garmin.OAuth2Token{AccessToken: "new"}); err != nil {
		t.Fatal(err)
	}

	got1, got2, err := garmin.LoadTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got1.OAuthToken != "t1" || got2.AccessToken != "new" {
		t.Errorf("got oauth1 %q oauth2 %q, want t1 new", got1.OAuthToken, got2.AccessToken)
	}
}

func TestLoadTokensErrors(t *testing.T) {
	t.Parallel()

	if _, _, err := garmin.LoadTokens(t.TempDir()); err == nil {
		t.Error("empty dir: got nil error")
	}

	onlyOAuth1 := t.TempDir()
	if err := garmin.SaveTokens(onlyOAuth1, &garmin.OAuth1Token{OAuthToken: "t1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := garmin.LoadTokens(onlyOAuth1); err == nil {
		t.Error("missing oauth2: got nil error")
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "oauth1_token.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := garmin.LoadTokens(broken); err == nil {
		t.Error("broken oauth1: got nil error")
	}
}

func TestOAuth2TokenExpiry(t *testing.T) {
	t.Parallel()

	pastSec := time.Now().Add(-time.Hour).Unix()
	futureSec := time.Now().Add(time.Hour).Unix()

	tok := garmin.OAuth2Token{ExpiresAt: pastSec, RefreshTokenExpiresAt: futureSec}
	if !tok.Expired() {
		t.Error("past ExpiresAt: Expired() = false, want true")
	}
	if tok.RefreshExpired() {
		t.Error("future RefreshTokenExpiresAt: RefreshExpired() = true, want false")
	}

	tok = garmin.OAuth2Token{ExpiresAt: futureSec, RefreshTokenExpiresAt: pastSec}
	if tok.Expired() {
		t.Error("future ExpiresAt: Expired() = true, want false")
	}
	if !tok.RefreshExpired() {
		t.Error("past RefreshTokenExpiresAt: RefreshExpired() = false, want true")
	}
}

func TestBearer(t *testing.T) {
	t.Parallel()

	if got := (&garmin.OAuth2Token{AccessToken: "abc"}).Bearer(); got != "Bearer abc" {
		t.Errorf("got %q", got)
	}
}
