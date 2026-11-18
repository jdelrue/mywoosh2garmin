// MyWhoosh2Garmin is a GUI app that uploads MyWhoosh activities to Garmin Connect.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"mywhoosh2garmin/garmin"
	"mywhoosh2garmin/mywhoosh"
)

// appConfig holds the user settings persisted to ~/.mywhoosh2garmin/config.json.
type appConfig struct {
	MyWhooshEmail string `json:"mywhoosh_email"`
	GarminEmail   string `json:"garmin_email"`
	Device        string `json:"device"`
	// Serial is the spoofed device serial number, random per install unless set by the user.
	Serial uint32 `json:"serial,omitempty"`
}

// appConfigDir returns ~/.mywhoosh2garmin, where config, tokens and the synced list are stored.
func appConfigDir() string {
	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".mywhoosh2garmin")
}

// loadAppConfig reads config.json, returning defaults when it is missing or broken.
func loadAppConfig() appConfig {
	var cfg appConfig
	data, err := os.ReadFile(filepath.Join(appConfigDir(), "config.json"))
	if err == nil {
		_ = json.Unmarshal(data, &cfg) // a broken config falls back to defaults
	}

	return cfg
}

// saveAppConfig is best effort: losing the config only means re-entering it.
func saveAppConfig(cfg appConfig) {
	dir := appConfigDir()
	_ = os.MkdirAll(dir, 0o700)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600)
}

// syncedTracker remembers which MyWhoosh activities were uploaded to Garmin,
// persisted to ~/.mywhoosh2garmin/synced.json.
type syncedTracker struct {
	mu       sync.Mutex
	path     string
	uploaded map[string]string // activityID → upload timestamp
}

func newSyncedTracker(dir string) *syncedTracker {
	st := &syncedTracker{
		path:     filepath.Join(dir, "synced.json"),
		uploaded: make(map[string]string),
	}
	data, err := os.ReadFile(st.path)
	if err == nil {
		_ = json.Unmarshal(data, &st.uploaded) // a broken file means nothing is marked synced
	}

	return st
}

func (st *syncedTracker) IsSynced(activityID string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.uploaded[activityID]

	return ok
}

func (st *syncedTracker) MarkSynced(activityID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.uploaded[activityID] = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(st.uploaded, "", "  ")
	if err != nil {
		return
	}
	// Best effort: if this fails, Garmin rejects the re-upload as a duplicate.
	_ = os.WriteFile(st.path, data, 0o600)
}

func main() {
	a := app.New()
	w := a.NewWindow("MyWhoosh2Garmin")
	w.Resize(fyne.NewSize(720, 700))

	// Cancel in-flight MyWhoosh and Garmin requests when the window closes.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.SetOnClosed(cancel)

	cfg := loadAppConfig()
	if cfg.Serial == 0 {
		cfg.Serial = randomSerial()
		saveAppConfig(cfg)
	}
	tracker := newSyncedTracker(appConfigDir())

	// --- MyWhoosh state ---
	mwClient := mywhoosh.NewClient(appConfigDir())

	// --- Garmin state ---
	var garminClient *garmin.Client

	// --- Log panel ---
	logEntry := widget.NewMultiLineEntry()
	logEntry.Wrapping = fyne.TextWrapWord
	logEntry.Disable()
	logScroll := container.NewVScroll(logEntry)
	logScroll.SetMinSize(fyne.NewSize(0, 140))

	var logMu sync.Mutex
	var logText string

	appendLog := func(msg string) {
		logMu.Lock()
		logText += msg + "\n"
		text := logText
		logMu.Unlock()
		fyne.Do(func() {
			logEntry.Enable()
			logEntry.SetText(text)
			logEntry.CursorRow = len(strings.Split(text, "\n"))
			logEntry.Disable()
			logScroll.ScrollToBottom()
		})
	}

	// Wire FIT processing logs into the GUI
	logFn = func(format string, args ...any) {
		appendLog(fmt.Sprintf(format, args...))
	}
	mwClient.Logf = logFn

	// --- MyWhoosh Credentials ---
	mwEmailEntry := widget.NewEntry()
	mwEmailEntry.SetPlaceHolder("MyWhoosh email")
	if cfg.MyWhooshEmail != "" {
		mwEmailEntry.SetText(cfg.MyWhooshEmail)
	}

	mwPasswordEntry := widget.NewPasswordEntry()
	mwPasswordEntry.SetPlaceHolder("MyWhoosh password")

	// --- Garmin Credentials ---
	garminEmailEntry := widget.NewEntry()
	garminEmailEntry.SetPlaceHolder("Garmin email")
	if cfg.GarminEmail != "" {
		garminEmailEntry.SetText(cfg.GarminEmail)
	}

	garminPasswordEntry := widget.NewPasswordEntry()
	garminPasswordEntry.SetPlaceHolder("Garmin password (only needed first time)")

	// --- Device to upload as ---
	deviceSelect := widget.NewSelect(spoofDeviceNames(), func(name string) {
		cfg.Device = name
		saveAppConfig(cfg)
	})
	deviceSelect.SetSelected(findSpoofDevice(cfg.Device).Name)

	selectedDevice := func() spoofDevice {
		device := findSpoofDevice(deviceSelect.Selected)
		device.Serial = cfg.Serial

		return device
	}

	// --- Activity list container ---
	activityListBox := container.NewVBox()
	activityScroll := container.NewVScroll(activityListBox)
	activityScroll.SetMinSize(fyne.NewSize(0, 280))

	mwStatusLabel := widget.NewLabel("")
	garminStatusLabel := widget.NewLabel("")

	// --- Helper: authenticate to Garmin ---
	ensureGarmin := func() (*garmin.Client, error) {
		if garminClient != nil && garminClient.OAuth2 != nil && !garminClient.OAuth2.Expired() {
			return garminClient, nil
		}

		tokenDir := appConfigDir()
		client := garmin.NewClient(tokenDir)

		if err := client.Resume(ctx); err == nil {
			garminClient = client

			return client, nil
		}

		email := garminEmailEntry.Text
		password := garminPasswordEntry.Text
		if email == "" || password == "" {
			return nil, errors.New("enter Garmin email & password for first login")
		}

		if err := client.Login(ctx, email, password); err != nil {
			return nil, fmt.Errorf("garmin login: %w", err)
		}

		garminClient = client

		return client, nil
	}

	// --- Helper: process and upload a single activity ---
	uploadActivity := func(activity mywhoosh.Activity, btn *widget.Button) {
		btn.Disable()
		btn.SetText("⏳")
		device := selectedDevice()

		go func() {
			defer func() {
				fyne.Do(func() {
					if tracker.IsSynced(activity.ID) {
						btn.SetText("✓")
						btn.Importance = widget.LowImportance
					} else {
						btn.SetText("⬆ Upload")
						btn.Enable()
					}
					btn.Refresh()
				})
			}()

			// 1. Download FIT from MyWhoosh
			appendLog(fmt.Sprintf("⬇ Downloading: %s (%s)…",
				activity.DisplayName(), activity.FormattedDate()))

			fileID := cmp.Or(activity.ActivityFileID, activity.ID)
			fitData, err := mwClient.DownloadFitFile(ctx, fileID)
			if err != nil {
				appendLog("  ❌ Download failed: " + err.Error())

				return
			}
			appendLog(fmt.Sprintf("  ✓ Downloaded %d bytes", len(fitData)))

			// 2. Save to temp file
			tmpDir := os.TempDir()
			ts := time.Now().Format("20060102_150405")
			inputPath := filepath.Join(tmpDir, fmt.Sprintf("mw_%s_%s.fit", activity.ID, ts))
			outputPath := filepath.Join(tmpDir, fmt.Sprintf("mw_%s_%s_fixed.fit", activity.ID, ts))

			if err := os.WriteFile(inputPath, fitData, 0o600); err != nil {
				appendLog("  ❌ Save temp file failed: " + err.Error())

				return
			}
			defer func() {
				_ = os.Remove(inputPath)
				_ = os.Remove(outputPath)
			}()

			// 3. Fix the FIT file
			appendLog("  🔧 Fixing FIT file…")
			if err := fixFitFile(inputPath, outputPath, device); err != nil {
				appendLog("  ❌ Fix failed: " + err.Error())

				return
			}

			// 4. Authenticate to Garmin
			client, err := ensureGarmin()
			if err != nil {
				appendLog("  ❌ " + err.Error())

				return
			}
			fyne.Do(func() {
				garminStatusLabel.SetText("✓ Garmin connected")
			})

			// 5. Upload to Garmin
			appendLog("  ⬆ Uploading to Garmin Connect…")
			if err := client.UploadFIT(ctx, outputPath); err != nil {
				if errors.Is(err, garmin.ErrDuplicate) {
					appendLog("  ⚠ Already on Garmin (duplicate)")
					tracker.MarkSynced(activity.ID)
				} else {
					appendLog("  ❌ Upload failed: " + err.Error())
				}

				return
			}

			tracker.MarkSynced(activity.ID)
			appendLog("  ✓ Uploaded to Garmin Connect!")
		}()
	}

	// --- Helper: build activity row widget ---
	buildActivityRow := func(activity mywhoosh.Activity) fyne.CanvasObject {
		dateStr := activity.FormattedDate()
		name := activity.DisplayName()

		details := dateStr + "  —  " + name
		if dist := activity.FormattedDistance(); dist != "" {
			details += "  •  " + dist
		}
		if dur := activity.FormattedDuration(); dur != "" {
			details += "  •  " + dur
		}
		if activity.AvgPower > 0 {
			details += fmt.Sprintf("  •  %.0fW", activity.AvgPower)
		}
		if activity.AvgHR > 0 {
			details += fmt.Sprintf("  •  %.0fbpm", activity.AvgHR)
		}

		infoLabel := widget.NewLabel(details)
		infoLabel.Wrapping = fyne.TextWrapWord

		uploadBtn := widget.NewButtonWithIcon("⬆ Upload", theme.UploadIcon(), nil)
		uploadBtn.Importance = widget.HighImportance

		if tracker.IsSynced(activity.ID) {
			uploadBtn.SetText("✓ Synced")
			uploadBtn.Importance = widget.LowImportance
			uploadBtn.Disable()
		}

		act := activity // capture for closure
		uploadBtn.OnTapped = func() {
			uploadActivity(act, uploadBtn)
		}

		return container.NewBorder(nil, nil, nil, uploadBtn, infoLabel)
	}

	// --- Upload All button ---
	uploadAllBtn := widget.NewButton("⬆  Upload All to Garmin", nil)
	uploadAllBtn.Importance = widget.HighImportance
	uploadAllBtn.Disable()

	var currentActivities []mywhoosh.Activity

	// --- Fetch Activities button ---
	var fetching bool
	fetchBtn := widget.NewButton("📋  Fetch Activities (last 10 days)", nil)
	fetchBtn.Importance = widget.HighImportance

	fetchBtn.OnTapped = func() {
		if fetching {
			return
		}
		fetching = true
		fetchBtn.Disable()

		go func() {
			defer func() {
				fetching = false
				fyne.Do(func() { fetchBtn.Enable() })
			}()

			// Persist config
			cfg.MyWhooshEmail = mwEmailEntry.Text
			cfg.GarminEmail = garminEmailEntry.Text
			saveAppConfig(cfg)

			// 1. Login to MyWhoosh (try cached session first)
			if err := mwClient.Resume(); err == nil {
				appendLog("MyWhoosh session resumed")
			} else {
				email := mwEmailEntry.Text
				password := mwPasswordEntry.Text
				if email == "" || password == "" {
					appendLog("❌ Enter MyWhoosh email & password")

					return
				}

				appendLog("Logging in to MyWhoosh…")
				if err := mwClient.Login(ctx, email, password); err != nil {
					appendLog("❌ MyWhoosh login failed: " + err.Error())

					return
				}
				appendLog("✓ Logged in to MyWhoosh")
			}
			fyne.Do(func() {
				mwStatusLabel.SetText("✓ MyWhoosh connected")
			})

			// 2. Fetch activities from last 10 days
			appendLog("Fetching activities (last 10 days)…")
			activities, err := mwClient.GetRecentActivities(ctx, 10)
			if err != nil {
				// Token may be expired — try fresh login
				email := mwEmailEntry.Text
				password := mwPasswordEntry.Text
				if email != "" && password != "" {
					appendLog("Session may be expired, retrying login…")
					if loginErr := mwClient.Login(ctx, email, password); loginErr == nil {
						activities, err = mwClient.GetRecentActivities(ctx, 10)
					}
				}
				if err != nil {
					appendLog("❌ Failed to fetch activities: " + err.Error())

					return
				}
			}

			if len(activities) == 0 {
				appendLog("No activities found in the last 10 days.")
				fyne.Do(func() {
					activityListBox.RemoveAll()
					activityListBox.Add(widget.NewLabel("No activities found in the last 10 days."))
				})

				return
			}

			appendLog(fmt.Sprintf("✓ Found %d activities", len(activities)))

			// 3. Build activity list UI
			currentActivities = activities

			fyne.Do(func() {
				activityListBox.RemoveAll()

				for _, act := range activities {
					row := buildActivityRow(act)
					activityListBox.Add(row)
				}

				// Count unsynced
				unsynced := 0
				for _, act := range activities {
					if !tracker.IsSynced(act.ID) {
						unsynced++
					}
				}

				if unsynced > 0 {
					uploadAllBtn.Enable()
					uploadAllBtn.SetText(fmt.Sprintf("⬆  Upload All to Garmin (%d new)", unsynced))
				} else {
					uploadAllBtn.SetText("✓  All synced")
					uploadAllBtn.Disable()
				}
			})
		}()
	}

	// --- Upload All logic ---
	var uploadingAll bool
	uploadAllBtn.OnTapped = func() {
		if uploadingAll {
			return
		}
		uploadingAll = true
		uploadAllBtn.Disable()
		device := selectedDevice()

		go func() {
			defer func() {
				uploadingAll = false
				fyne.Do(func() {
					unsynced := 0
					for _, act := range currentActivities {
						if !tracker.IsSynced(act.ID) {
							unsynced++
						}
					}
					if unsynced > 0 {
						uploadAllBtn.SetText(fmt.Sprintf("⬆  Upload All to Garmin (%d remaining)", unsynced))
						uploadAllBtn.Enable()
					} else {
						uploadAllBtn.SetText("✓  All synced")
					}
				})
			}()

			success, failed := 0, 0
			for i, act := range currentActivities {
				if tracker.IsSynced(act.ID) {
					continue
				}

				appendLog(fmt.Sprintf("\n[%d/%d] %s", i+1, len(currentActivities), act.DisplayName()))

				fileID := cmp.Or(act.ActivityFileID, act.ID)

				// Download
				fitData, err := mwClient.DownloadFitFile(ctx, fileID)
				if err != nil {
					appendLog("  ❌ Download failed: " + err.Error())
					failed++

					continue
				}

				// Save temp
				tmpDir := os.TempDir()
				ts := time.Now().Format("20060102_150405")
				inputPath := filepath.Join(tmpDir, fmt.Sprintf("mw_%s_%s.fit", act.ID, ts))
				outputPath := filepath.Join(tmpDir, fmt.Sprintf("mw_%s_%s_fixed.fit", act.ID, ts))

				if err := os.WriteFile(inputPath, fitData, 0o600); err != nil {
					appendLog("  ❌ Save failed: " + err.Error())
					failed++

					continue
				}

				// Fix
				if err := fixFitFile(inputPath, outputPath, device); err != nil {
					appendLog("  ❌ Fix failed: " + err.Error())
					_ = os.Remove(inputPath)
					failed++

					continue
				}

				// Garmin auth
				client, err := ensureGarmin()
				if err != nil {
					appendLog("  ❌ " + err.Error())
					_ = os.Remove(inputPath)
					_ = os.Remove(outputPath)
					failed++

					continue
				}

				// Upload
				if err := client.UploadFIT(ctx, outputPath); err != nil {
					if errors.Is(err, garmin.ErrDuplicate) {
						appendLog("  ⚠ Already on Garmin (duplicate)")
						tracker.MarkSynced(act.ID)
						success++
					} else {
						appendLog("  ❌ Upload failed: " + err.Error())
						failed++
					}
				} else {
					tracker.MarkSynced(act.ID)
					success++
					appendLog("  ✓ Uploaded")
				}

				_ = os.Remove(inputPath)
				_ = os.Remove(outputPath)
			}

			appendLog(fmt.Sprintf("\n✓ Batch complete — %d uploaded, %d failed", success, failed))

			// Refresh the activity list UI
			fyne.Do(func() {
				activityListBox.RemoveAll()
				for _, act := range currentActivities {
					row := buildActivityRow(act)
					activityListBox.Add(row)
				}
			})
		}()
	}

	// --- Layout ---
	title := widget.NewRichTextFromMarkdown("## MyWhoosh → Garmin")

	mywhooshSection := container.NewVBox(
		widget.NewLabel("MyWhoosh Account"),
		mwEmailEntry,
		mwPasswordEntry,
		mwStatusLabel,
	)

	garminSection := container.NewVBox(
		widget.NewLabel("Garmin Connect"),
		garminEmailEntry,
		garminPasswordEntry,
		garminStatusLabel,
	)

	credentialsRow := container.New(layout.NewGridWrapLayout(fyne.NewSize(340, 160)),
		mywhooshSection, garminSection,
	)

	activitiesHeader := container.NewBorder(nil, nil, nil,
		uploadAllBtn,
		widget.NewRichTextFromMarkdown("### Activities (last 10 days)"),
	)

	topForm := container.NewVBox(
		title,
		widget.NewSeparator(),
		credentialsRow,
		widget.NewSeparator(),
		container.NewBorder(nil, nil, widget.NewLabel("Upload as device"), nil, deviceSelect),
		fetchBtn,
		widget.NewSeparator(),
		activitiesHeader,
	)

	content := container.NewBorder(topForm, logScroll, nil, nil, activityScroll)
	w.SetContent(content)
	w.ShowAndRun()
}
