package mywhoosh_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mywhoosh2garmin/mywhoosh"
)

func TestFormattedDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"01:31:54.000", "1h31m54s"},
		{"10:00:05.000", "10h00m05s"},
		{"00:45:10.000", "45m10s"},
		{"00:45:10", "45m10s"},
		{"", ""},
		{"45:10", "45:10"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			a := mywhoosh.Activity{RideDuration: tt.in}
			if got := a.FormattedDuration(); got != tt.want {
				t.Errorf("FormattedDuration(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormattedDistance(t *testing.T) {
	t.Parallel()

	if got := (&mywhoosh.Activity{Distance: 42.56}).FormattedDistance(); got != "42.6 km" {
		t.Errorf("got %q, want %q", got, "42.6 km")
	}
	if got := (&mywhoosh.Activity{}).FormattedDistance(); got != "" {
		t.Errorf("zero distance: got %q, want empty", got)
	}
}

func TestDisplayName(t *testing.T) {
	t.Parallel()

	if got := (&mywhoosh.Activity{Name: "Alpe du Zwift"}).DisplayName(); got != "Alpe du Zwift" {
		t.Errorf("got %q", got)
	}
	if got := (&mywhoosh.Activity{}).DisplayName(); got != "Activity" {
		t.Errorf("empty name: got %q, want %q", got, "Activity")
	}
}

// TestFormattedDate is not parallel because it sets time.Local.
func TestFormattedDate(t *testing.T) {
	// A fixed non-UTC zone makes a missing .Local() conversion visible.
	orig := time.Local                            //nolint:gosmopolitan // saved to restore after the test
	time.Local = time.FixedZone("UTC+2", 2*60*60) //nolint:gosmopolitan,reassign // see above
	t.Cleanup(func() { time.Local = orig })       //nolint:gosmopolitan,reassign // restore

	date := float64(time.Date(2025, time.December, 1, 8, 0, 0, 0, time.UTC).Unix())

	tests := []struct {
		name     string
		activity mywhoosh.Activity
		want     string
	}{
		{
			name:     "start datetime",
			activity: mywhoosh.Activity{StartDatetime: "2026-03-04T17:30:00.000Z", Date: date},
			want:     "Wed 04 Mar 2026 19:30",
		},
		{
			name:     "invalid start datetime falls back to date",
			activity: mywhoosh.Activity{StartDatetime: "yesterday", Date: date},
			want:     "Mon 01 Dec 2025 10:00",
		},
		{
			name:     "date only",
			activity: mywhoosh.Activity{Date: date},
			want:     "Mon 01 Dec 2025 10:00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.activity.FormattedDate(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResume(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	token := `{"access_token":"access","whoosh_id":"w42"}`
	if err := os.WriteFile(filepath.Join(dir, "mywhoosh_token.json"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}

	c := mywhoosh.NewClient(dir)
	if err := c.Resume(); err != nil {
		t.Fatal(err)
	}
	if !c.IsAuthenticated() {
		t.Error("resumed client not authenticated")
	}
}

func TestResumeErrors(t *testing.T) {
	t.Parallel()

	if err := mywhoosh.NewClient("").Resume(); err == nil {
		t.Error("no token dir: got nil error")
	}
	if err := mywhoosh.NewClient(t.TempDir()).Resume(); err == nil {
		t.Error("missing file: got nil error")
	}

	for name, content := range map[string]string{
		"broken JSON": "{broken",
		"empty token": `{"access_token":"","whoosh_id":"w42"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "mywhoosh_token.json"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			c := mywhoosh.NewClient(dir)
			if err := c.Resume(); err == nil {
				t.Error("got nil error")
			}
			if c.IsAuthenticated() {
				t.Error("client authenticated")
			}
		})
	}
}

func TestNotAuthenticated(t *testing.T) {
	t.Parallel()

	c := mywhoosh.NewClient(t.TempDir())
	if c.IsAuthenticated() {
		t.Error("new client authenticated")
	}
	if _, err := c.GetActivities(t.Context()); err == nil {
		t.Error("GetActivities: got nil error")
	}
	if _, err := c.GetRecentActivities(t.Context(), 10); err == nil {
		t.Error("GetRecentActivities: got nil error")
	}
	if _, err := c.DownloadFitFile(t.Context(), "file"); err == nil {
		t.Error("DownloadFitFile: got nil error")
	}
}
