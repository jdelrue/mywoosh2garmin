// Package garmin authenticates with Garmin Connect and uploads FIT activity files.
package garmin

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const apiUserAgent = "GCM-iOS-5.19.1.2"

// ErrDuplicate is returned by UploadFIT when Garmin already has the activity.
var ErrDuplicate = errors.New("duplicate activity (already uploaded to Garmin)")

// Client manages authentication and uploads to Garmin Connect.
type Client struct {
	OAuth1   *OAuth1Token
	OAuth2   *OAuth2Token
	Domain   string
	TokenDir string // directory where tokens are cached
}

// NewClient creates a Client that caches tokens in tokenDir.
func NewClient(tokenDir string) *Client {
	return &Client{
		Domain:   "garmin.com",
		TokenDir: tokenDir,
	}
}

// Resume tries to load cached tokens and refresh if needed.
// Returns nil if a valid session was restored, error otherwise.
func (c *Client) Resume(ctx context.Context) error {
	if c.TokenDir == "" {
		return errors.New("no token directory configured")
	}

	oauth1, oauth2, err := LoadTokens(c.TokenDir)
	if err != nil {
		return fmt.Errorf("no cached session: %w", err)
	}

	c.OAuth1 = oauth1
	c.OAuth2 = oauth2
	c.Domain = cmp.Or(oauth1.Domain, c.Domain)

	// Token still valid
	if !oauth2.Expired() {
		return nil
	}

	// OAuth2 expired — try to refresh using OAuth1 (lasts ~1 year)
	fmt.Println("  session expired, refreshing...")
	if err := c.refreshOAuth2(ctx); err != nil {
		return fmt.Errorf("refresh failed: %w", err)
	}

	return nil
}

// Login performs a fresh SSO login with the given credentials.
func (c *Client) Login(ctx context.Context, email, password string) error {
	oauth1, oauth2, err := Login(ctx, email, password, c.Domain)
	if err != nil {
		return err
	}

	c.OAuth1 = oauth1
	c.OAuth2 = oauth2

	// Cache tokens for next time
	if c.TokenDir != "" {
		if err := SaveTokens(c.TokenDir, oauth1, oauth2); err != nil {
			fmt.Printf("  warning: could not cache tokens: %v\n", err)
		}
	}

	return nil
}

// UploadFIT uploads a FIT file to Garmin Connect.
// Automatically refreshes the OAuth2 token if expired.
func (c *Client) UploadFIT(ctx context.Context, filePath string) error {
	if c.OAuth2 == nil {
		return errors.New("not authenticated")
	}

	// Auto-refresh if expired
	if c.OAuth2.Expired() {
		if err := c.refreshOAuth2(ctx); err != nil {
			return fmt.Errorf("token refresh: %w", err)
		}
	}

	// First attempt
	status, body, err := c.doUpload(ctx, filePath)
	if err != nil {
		return err
	}

	// Retry once on 401 (token might be stale despite not being expired)
	if status == 401 {
		fmt.Println("  token rejected, refreshing...")
		if err := c.refreshOAuth2(ctx); err != nil {
			return fmt.Errorf("token refresh: %w", err)
		}
		status, body, err = c.doUpload(ctx, filePath)
		if err != nil {
			return err
		}
	}

	return parseUploadResult(status, body)
}

// refreshOAuth2 exchanges the OAuth1 token for a fresh OAuth2 token.
func (c *Client) refreshOAuth2(ctx context.Context) error {
	oauth2, err := ExchangeForOAuth2(ctx, c.OAuth1)
	if err != nil {
		return err
	}
	c.OAuth2 = oauth2

	// Update cache (only OAuth2, keep OAuth1 as-is)
	if c.TokenDir != "" {
		_ = SaveTokens(c.TokenDir, nil, oauth2)
	}

	return nil
}

// doUpload performs the actual multipart upload and returns status + body.
func (c *Client) doUpload(ctx context.Context, filePath string) (int, []byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()

	// Build multipart request
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return 0, nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return 0, nil, err
	}
	if err := writer.Close(); err != nil {
		return 0, nil, err
	}

	uploadURL := fmt.Sprintf("https://connectapi.%s/upload-service/upload", c.Domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", c.OAuth2.Bearer())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", apiUserAgent)
	req.Header.Set("Di-Backend", "connectapi."+c.Domain)
	req.Header.Set("Nk", "NT")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}

	return resp.StatusCode, body, nil
}

// parseUploadResult checks the upload response for errors.
func parseUploadResult(status int, body []byte) error {
	if status == http.StatusConflict {
		return ErrDuplicate
	}
	if status >= 400 {
		return fmt.Errorf("upload failed (HTTP %d): %s", status,
			string(body[:min(300, len(body))]))
	}

	// Check for failures in the detailed result
	var result struct {
		DetailedImportResult struct {
			Failures  []any `json:"failures"`
			Successes []any `json:"successes"`
		} `json:"detailedImportResult"`
	}
	if err := json.Unmarshal(body, &result); err == nil {
		if len(result.DetailedImportResult.Failures) > 0 {
			return fmt.Errorf("upload reported failures: %v",
				result.DetailedImportResult.Failures)
		}
	}

	return nil
}
