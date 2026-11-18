// Package mywhoosh is a client for the MyWhoosh API: login, activity listing and FIT file download.
package mywhoosh

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	loginURL       = "https://services.mywhoosh.com/http-service/api/login"
	activitiesBase = "https://service14.mywhoosh.com/v2/"
)

// Activity represents a single MyWhoosh ride activity.
type Activity struct {
	ID             string  `json:"id"`
	Name           string  `json:"title"`
	Date           float64 `json:"date"`
	ActivityFileID string  `json:"activityFileId"`
	Distance       float64 `json:"distance,omitempty"`
	RideDuration   string  `json:"rideDuration,omitempty"`
	AvgPower       float64 `json:"watt,omitempty"`
	AvgHR          float64 `json:"heartrate,omitempty"`
	Elevation      float64 `json:"elevation,omitempty"`
	WattPerKg      float64 `json:"wattPerKg,omitempty"`
	RouteName      string  `json:"routeName,omitempty"`
	SportType      string  `json:"sportType,omitempty"`
	StartDatetime  string  `json:"startDatetime,omitempty"`
}

// FormattedDate returns the activity date as a human-readable string.
func (a *Activity) FormattedDate() string {
	// Try parsing startDatetime first (more precise)
	if a.StartDatetime != "" {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", a.StartDatetime); err == nil {
			//nolint:gosmopolitan // show the ride time in the user's time zone
			return t.Local().Format("Mon 02 Jan 2006 15:04")
		}
	}
	t := time.Unix(int64(a.Date), 0)

	return t.Format("Mon 02 Jan 2006 15:04")
}

// FormattedDuration returns the ride duration as a human-readable string.
func (a *Activity) FormattedDuration() string {
	// rideDuration comes as "01:31:54.000"
	if a.RideDuration == "" {
		return ""
	}
	parts := strings.Split(strings.Split(a.RideDuration, ".")[0], ":")
	if len(parts) == 3 {
		h, m, s := parts[0], parts[1], parts[2]
		if h == "00" {
			return m + "m" + s + "s"
		}
		// Strip leading zero from hours
		if len(h) > 1 && h[0] == '0' {
			h = h[1:]
		}

		return h + "h" + m + "m" + s + "s"
	}

	return a.RideDuration
}

// FormattedDistance returns the distance in km with one decimal.
func (a *Activity) FormattedDistance() string {
	// distance is already in km
	if a.Distance > 0 {
		return fmt.Sprintf("%.1f km", a.Distance)
	}

	return ""
}

// DisplayName returns a useful display name for the activity.
func (a *Activity) DisplayName() string {
	return cmp.Or(a.Name, "Activity")
}

// DateUnix returns the activity date as a Unix timestamp.
func (a *Activity) DateUnix() int64 {
	return int64(a.Date)
}

// Client manages authentication and API calls to MyWhoosh.
type Client struct {
	// Logf reports non-fatal problems, such as a failure to cache the session token.
	Logf func(format string, args ...any)

	token      string
	whooshID   string
	tokenDir   string
	httpClient *http.Client
}

// cachedToken is the on-disk format for a saved MyWhoosh session.
type cachedToken struct {
	AccessToken string `json:"access_token"`
	WhooshID    string `json:"whoosh_id"`
}

// NewClient creates a new MyWhoosh API client.
// tokenDir is the directory where the session token is cached (e.g. ~/.mywhoosh2garmin/).
func NewClient(tokenDir string) *Client {
	return &Client{
		Logf:       log.Printf,
		tokenDir:   tokenDir,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Resume tries to restore a cached MyWhoosh session.
// Returns nil if a valid token was loaded, error otherwise.
func (c *Client) Resume() error {
	if c.tokenDir == "" {
		return errors.New("no token directory")
	}
	data, err := os.ReadFile(filepath.Join(c.tokenDir, "mywhoosh_token.json"))
	if err != nil {
		return fmt.Errorf("no cached session: %w", err)
	}
	var ct cachedToken
	if err := json.Unmarshal(data, &ct); err != nil {
		return fmt.Errorf("parse cached token: %w", err)
	}
	if ct.AccessToken == "" {
		return errors.New("empty cached token")
	}
	c.token = ct.AccessToken
	c.whooshID = ct.WhooshID

	return nil
}

// saveToken persists the current session token to disk.
func (c *Client) saveToken() error {
	if c.tokenDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.tokenDir, 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	ct := cachedToken{AccessToken: c.token, WhooshID: c.whooshID}
	data, err := json.MarshalIndent(ct, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	tokenPath := filepath.Join(c.tokenDir, "mywhoosh_token.json")
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}

	return nil
}

// Login authenticates with MyWhoosh using email and password.
func (c *Client) Login(ctx context.Context, email, password string) error {
	payload := map[string]any{
		"Username":      email,
		"Password":      password,
		"Platform":      "Android",
		"Action":        1001,
		"CorrelationId": uuid.New().String(),
		"DeviceId":      uuid.New().String(),
		"Authorization": "",
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode,
			string(body[:min(300, len(body))]))
	}

	var result struct {
		Success      bool   `json:"Success"`
		Message      string `json:"Message"`
		AccessToken  string `json:"AccessToken"`
		RefreshToken string `json:"RefreshToken"`
		WhooshID     string `json:"WhooshId"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse login response: %w", err)
	}

	if !result.Success {
		msg := cmp.Or(result.Message, "unknown error")

		return fmt.Errorf("login failed: %s", msg)
	}

	if result.AccessToken == "" {
		return errors.New("login failed: no access token in response")
	}

	c.token = result.AccessToken
	c.whooshID = result.WhooshID
	if err := c.saveToken(); err != nil {
		c.Logf("could not cache MyWhoosh session, you will need to log in again next time: %v", err)
	}

	return nil
}

// IsAuthenticated returns true if the client has a valid token.
func (c *Client) IsAuthenticated() bool {
	return c.token != ""
}

// GetActivities fetches all activities, paginated. Returns newest first.
func (c *Client) GetActivities(ctx context.Context) ([]Activity, error) {
	if c.token == "" {
		return nil, errors.New("not authenticated — login first")
	}

	activitiesURL := activitiesBase + "rider/profile/activities"
	var allActivities []Activity
	page := 1

	for {
		payload := fmt.Sprintf(`{"sortDate":"DESC","page":%d}`, page)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, activitiesURL, strings.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("activities request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("fetch activities failed (HTTP %d): %s",
				resp.StatusCode, string(body[:min(300, len(body))]))
		}

		var result struct {
			Data struct {
				Results    []json.RawMessage `json:"results"`
				TotalPages int               `json:"totalPages"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse activities: %w", err)
		}

		for _, rawAct := range result.Data.Results {
			var act Activity
			if err := json.Unmarshal(rawAct, &act); err == nil {
				allActivities = append(allActivities, act)
			}
		}

		if page >= result.Data.TotalPages {
			break
		}
		page++
	}

	return allActivities, nil
}

// GetRecentActivities returns activities from the last N days.
func (c *Client) GetRecentActivities(ctx context.Context, days int) ([]Activity, error) {
	all, err := c.GetActivities(ctx)
	if err != nil {
		return nil, err
	}

	cutoffSec := time.Now().AddDate(0, 0, -days).Unix()
	var recent []Activity
	for _, a := range all {
		// Activities are sorted DESC, so once we pass the cutoff we can stop
		if a.DateUnix() < cutoffSec {
			break
		}
		recent = append(recent, a)
	}

	return recent, nil
}

// DownloadFitFile downloads the FIT file for a given activity and returns the raw bytes.
func (c *Client) DownloadFitFile(ctx context.Context, activityFileID string) ([]byte, error) {
	if c.token == "" {
		return nil, errors.New("not authenticated — login first")
	}

	downloadURL := activitiesBase + "rider/profile/download-activity-file"
	payload, err := json.Marshal(map[string]string{"fileId": activityFileID})
	if err != nil {
		return nil, fmt.Errorf("marshal download payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, downloadURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download failed (HTTP %d): %s",
			resp.StatusCode, string(body[:min(300, len(body))]))
	}

	// Response contains a pre-signed S3 URL
	var result struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse download response: %w", err)
	}

	if result.Data == "" {
		return nil, errors.New("no download URL in response")
	}

	// Fetch the actual FIT file from the S3 URL
	fitReq, err := http.NewRequestWithContext(ctx, http.MethodGet, result.Data, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create FIT request: %w", err)
	}
	fitResp, err := c.httpClient.Do(fitReq)
	if err != nil {
		return nil, fmt.Errorf("fetch FIT file: %w", err)
	}
	defer fitResp.Body.Close()

	fitData, err := io.ReadAll(fitResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read FIT file: %w", err)
	}

	if fitResp.StatusCode >= 400 {
		return nil, fmt.Errorf("FIT download failed (HTTP %d)", fitResp.StatusCode)
	}

	return fitData, nil
}
