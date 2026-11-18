package garmin_test

import (
	"testing"
	"time"

	"mywhoosh2garmin/garmin"
)

func TestResumeValidToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oauth1 := &garmin.OAuth1Token{OAuthToken: "t1", Domain: "garmin.cn"}
	oauth2 := &garmin.OAuth2Token{AccessToken: "a2", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := garmin.SaveTokens(dir, oauth1, oauth2); err != nil {
		t.Fatal(err)
	}

	c := garmin.NewClient(dir)
	if err := c.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.Domain != "garmin.cn" || c.OAuth2.AccessToken != "a2" {
		t.Errorf("got domain %q access token %q, want garmin.cn a2", c.Domain, c.OAuth2.AccessToken)
	}
}

func TestResumeErrors(t *testing.T) {
	t.Parallel()

	if err := garmin.NewClient("").Resume(t.Context()); err == nil {
		t.Error("no token dir: got nil error")
	}
	if err := garmin.NewClient(t.TempDir()).Resume(t.Context()); err == nil {
		t.Error("no cached tokens: got nil error")
	}
}

func TestUploadFITNotAuthenticated(t *testing.T) {
	t.Parallel()

	if err := garmin.NewClient(t.TempDir()).UploadFIT(t.Context(), "activity.fit"); err == nil {
		t.Error("got nil error")
	}
}
