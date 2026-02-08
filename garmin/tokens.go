package garmin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OAuth1Token represents the OAuth1 credentials obtained from Garmin SSO.
// The OAuth1 token typically lasts ~1 year.
type OAuth1Token struct {
	OAuthToken       string `json:"oauth_token"`
	OAuthTokenSecret string `json:"oauth_token_secret"`
	MFAToken         string `json:"mfa_token,omitempty"`
	MFAExpiration    string `json:"mfa_expiration_timestamp,omitempty"`
	Domain           string `json:"domain,omitempty"`
}

// OAuth2Token represents the short-lived Bearer token used for API access.
type OAuth2Token struct {
	Scope                 string `json:"scope"`
	Jti                   string `json:"jti"`
	TokenType             string `json:"token_type"`
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	ExpiresAt             int64  `json:"expires_at"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	RefreshTokenExpiresAt int64  `json:"refresh_token_expires_at"`
}

// Expired returns true if the access token has expired.
func (t *OAuth2Token) Expired() bool {
	return t.ExpiresAt < time.Now().Unix()
}

// RefreshExpired returns true if the refresh token has expired.
func (t *OAuth2Token) RefreshExpired() bool {
	return t.RefreshTokenExpiresAt < time.Now().Unix()
}

// Bearer returns the Authorization header value.
func (t *OAuth2Token) Bearer() string {
	return fmt.Sprintf("Bearer %s", t.AccessToken)
}

// SaveTokens saves OAuth1 and/or OAuth2 tokens to the given directory.
func SaveTokens(dir string, oauth1 *OAuth1Token, oauth2 *OAuth2Token) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if oauth1 != nil {
		data, err := json.MarshalIndent(oauth1, "", "    ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "oauth1_token.json"), data, 0o600); err != nil {
			return err
		}
	}
	if oauth2 != nil {
		data, err := json.MarshalIndent(oauth2, "", "    ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "oauth2_token.json"), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// LoadTokens loads OAuth1 and OAuth2 tokens from the given directory.
func LoadTokens(dir string) (*OAuth1Token, *OAuth2Token, error) {
	oauth1Data, err := os.ReadFile(filepath.Join(dir, "oauth1_token.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("load oauth1: %w", err)
	}
	var oauth1 OAuth1Token
	if err := json.Unmarshal(oauth1Data, &oauth1); err != nil {
		return nil, nil, fmt.Errorf("parse oauth1: %w", err)
	}

	oauth2Data, err := os.ReadFile(filepath.Join(dir, "oauth2_token.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("load oauth2: %w", err)
	}
	var oauth2 OAuth2Token
	if err := json.Unmarshal(oauth2Data, &oauth2); err != nil {
		return nil, nil, fmt.Errorf("parse oauth2: %w", err)
	}

	return &oauth1, &oauth2, nil
}
