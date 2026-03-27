package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"mywhoosh2garmin/garmin"
	"mywhoosh2garmin/mywhoosh"
)

// ---------------------------------------------------------------------------
// App Configuration
// ---------------------------------------------------------------------------

type appConfig struct {
	MyWhooshEmail    string `json:"mywhoosh_email"`
	MyWhooshPassword string `json:"mywhoosh_password"`
	GarminEmail      string `json:"garmin_email"`
	GarminPassword   string `json:"garmin_password"`
	GarminUnitID     string `json:"garmin_unit_id"`
	Automate         bool   `json:"automate"`
}

func appConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mywhoosh2garmin")
}

func loadAppConfig() appConfig {
	var cfg appConfig
	data, err := os.ReadFile(filepath.Join(appConfigDir(), "config.json"))
	if err == nil {
		json.Unmarshal(data, &cfg)
	}
	return cfg
}

func saveAppConfig(cfg appConfig) {
	dir := appConfigDir()
	os.MkdirAll(dir, 0o700)
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600)
}

// ---------------------------------------------------------------------------
// Automation & Background Logic
// ---------------------------------------------------------------------------

func registerWindowsTask() error {
	exePath, _ := os.Executable()
	cmd := exec.Command("schtasks", "/create", "/f",
		"/tn", "MyWhoosh2GarminSync",
		"/tr", fmt.Sprintf("\"%s\" -silent", exePath),
		"/sc", "daily",
		"/st", "21:00")
	return cmd.Run()
}

func unregisterWindowsTask() {
	exec.Command("schtasks", "/delete", "/f", "/tn", "MyWhoosh2GarminSync").Run()
}

func runSilentSync() {
	cfg := loadAppConfig()
	tracker := newSyncedTracker(appConfigDir())
	mwClient := mywhoosh.NewClient(appConfigDir())

	if err := mwClient.Resume(); err != nil {
		mwClient.Login(cfg.MyWhooshEmail, cfg.MyWhooshPassword)
	}

	activities, _ := mwClient.GetRecentActivities(10)
	garminClient := garmin.NewClient(appConfigDir())
	garminClient.Resume()

	for _, act := range activities {
		if tracker.IsSynced(act.ID) {
			continue
		}
		fitData, _ := mwClient.DownloadFitFile(act.ID)
		tmpIn := filepath.Join(os.TempDir(), "in.fit")
		tmpOut := filepath.Join(os.TempDir(), "out.fit")
		os.WriteFile(tmpIn, fitData, 0600)
		if err := fixFitFile(tmpIn, tmpOut, cfg.GarminUnitID); err == nil {
			if err := garminClient.UploadFIT(tmpOut); err == nil {
				tracker.MarkSynced(act.ID)
			}
		}
		os.Remove(tmpIn)
		os.Remove(tmpOut)
	}
}

// ---------------------------------------------------------------------------
// Sync Tracker
// ---------------------------------------------------------------------------

type syncedTracker struct {
	mu       sync.Mutex
	path     string
	uploaded map[string]string
}

func newSyncedTracker(dir string) *syncedTracker {
	st := &syncedTracker{
		path:     filepath.Join(dir, "synced.json"),
		uploaded: make(map[string]string),
	}
	data, err := os.ReadFile(st.path)
	if err == nil {
		json.Unmarshal(data, &st.uploaded)
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
	data, _ := json.MarshalIndent(st.uploaded, "", "  ")
	os.WriteFile(st.path, data, 0o600)
}

// ---------------------------------------------------------------------------
// Main GUI
// ---------------------------------------------------------------------------

func main() {
	silentMode := flag.Bool("silent", false, "Run in background sync mode")
	flag.Parse()

	if *silentMode {
		runSilentSync()
		return
	}

	a := app.New()
	w := a.NewWindow("MyWhoosh2Garmin - Activity Sync")

	cfg := loadAppConfig()
	tracker := newSyncedTracker(appConfigDir())
	mwClient := mywhoosh.NewClient(appConfigDir())

	// --- LOGS ---
	logLabel := widget.NewLabel("")
	logLabel.Wrapping = fyne.TextWrapWord
	logScroll := container.NewVScroll(logLabel)
	logScroll.SetMinSize(fyne.NewSize(0, 150))

	var logMu sync.Mutex
	var logText string
	appendLog := func(msg string) {
		logMu.Lock()
		logText += msg + "\n"
		text := logText
		logMu.Unlock()
		fyne.Do(func() {
			logLabel.SetText(text)
			logScroll.ScrollToBottom()
		})
	}

	logFn = func(format string, args ...interface{}) {
		appendLog(fmt.Sprintf(format, args...))
	}

	// --- INPUTS (Setting saved values here) ---
	mwEmail := widget.NewEntry()
	mwEmail.SetText(cfg.MyWhooshEmail)
	mwEmail.PlaceHolder = "Email"

	mwPass := widget.NewPasswordEntry()
	mwPass.SetText(cfg.MyWhooshPassword)
	mwPass.PlaceHolder = "Password"

	garminEmail := widget.NewEntry()
	garminEmail.SetText(cfg.GarminEmail)
	garminEmail.PlaceHolder = "Email"

	garminPass := widget.NewPasswordEntry()
	garminPass.SetText(cfg.GarminPassword)
	garminPass.PlaceHolder = "Password"

	garminID := widget.NewEntry()
	garminID.SetText(cfg.GarminUnitID)
	garminID.PlaceHolder = "10-Digit Unit ID"

	mwStatus := widget.NewLabel("")
	garminStatus := widget.NewLabel("")

	activityListBox := container.NewVBox()
	activityScroll := container.NewVScroll(activityListBox)

	// --- HELPER: Save All UI to Config ---
	syncUIToConfig := func() {
		cfg.MyWhooshEmail = mwEmail.Text
		cfg.MyWhooshPassword = mwPass.Text
		cfg.GarminEmail = garminEmail.Text
		cfg.GarminPassword = garminPass.Text
		cfg.GarminUnitID = garminID.Text
		saveAppConfig(cfg)
	}

	// --- AUTOMATION TOGGLE ---
	automateCheck := widget.NewCheck("Automate Background Sync (Daily 9PM)", func(checked bool) {
		syncUIToConfig() // Save everything when toggled
		cfg.Automate = checked
		if checked {
			registerWindowsTask()
			appendLog("✅ Daily sync scheduled.")
		} else {
			unregisterWindowsTask()
			appendLog("🚫 Automation disabled.")
		}
		saveAppConfig(cfg)
	})
	automateCheck.SetChecked(cfg.Automate)

	// --- UPLOAD LOGIC ---
	uploadActivity := func(activity mywhoosh.Activity, btn *widget.Button) {
		if btn != nil {
			btn.Disable()
			btn.SetText("⏳")
		}
		go func() {
			defer func() {
				fyne.Do(func() {
					if btn != nil {
						if tracker.IsSynced(activity.ID) {
							btn.SetText("✓")
							btn.Importance = widget.LowImportance
						} else {
							btn.SetText("⬆ Upload")
							btn.Enable()
						}
						btn.Refresh()
					}
				})
			}()

			appendLog("⬇ Downloading: " + activity.DisplayName())
			fitData, err := mwClient.DownloadFitFile(activity.ID)
			if err != nil {
				appendLog("❌ Error: " + err.Error())
				return
			}

			tmpIn := filepath.Join(os.TempDir(), "in.fit")
			tmpOut := filepath.Join(os.TempDir(), "out.fit")
			os.WriteFile(tmpIn, fitData, 0600)
			defer os.Remove(tmpIn)
			defer os.Remove(tmpOut)

			if err := fixFitFile(tmpIn, tmpOut, garminID.Text); err != nil {
				appendLog("❌ Fix failed: " + err.Error())
				return
			}

			gClient := garmin.NewClient(appConfigDir())
			if err := gClient.Resume(); err != nil {
				gClient.Login(garminEmail.Text, garminPass.Text)
			}

			if err := gClient.UploadFIT(tmpOut); err != nil {
				if strings.Contains(err.Error(), "duplicate") {
					tracker.MarkSynced(activity.ID)
					appendLog("⚠ Duplicate ignored.")
				} else {
					appendLog("❌ Upload failed: " + err.Error())
				}
				return
			}
			tracker.MarkSynced(activity.ID)
			appendLog("✓ Uploaded!")
		}()
	}

	buildRow := func(act mywhoosh.Activity) fyne.CanvasObject {
		name := widget.NewRichTextFromMarkdown("**" + act.DisplayName() + "**")
		date := widget.NewLabel(act.FormattedDate())
		btn := widget.NewButtonWithIcon("⬆ Upload", theme.UploadIcon(), nil)
		btn.Importance = widget.HighImportance
		if tracker.IsSynced(act.ID) {
			btn.SetText("✓ Synced")
			btn.Importance = widget.LowImportance
			btn.Disable()
		}
		btn.OnTapped = func() { uploadActivity(act, btn) }
		return container.NewVBox(container.NewPadded(container.NewBorder(nil, nil, nil, btn, container.NewVBox(name, date))))
	}

	uploadAllBtn := widget.NewButtonWithIcon("Upload All", theme.UploadIcon(), nil)
	uploadAllBtn.Importance = widget.HighImportance
	uploadAllBtn.Disable()

	var currentActivities []mywhoosh.Activity
	fetchBtn := widget.NewButtonWithIcon("Fetch Activities", theme.SearchIcon(), func() {
		syncUIToConfig() // Save values before fetching
		go func() {
			if err := mwClient.Resume(); err != nil {
				mwClient.Login(mwEmail.Text, mwPass.Text)
			}
			fyne.Do(func() { mwStatus.SetText("✓ MyWhoosh connected") })
			activities, _ := mwClient.GetRecentActivities(20)
			currentActivities = activities
			fyne.Do(func() {
				activityListBox.RemoveAll()
				if len(activities) > 0 {
					uploadAllBtn.Enable()
				}
				for _, act := range activities {
					activityListBox.Add(buildRow(act))
				}
				appendLog(fmt.Sprintf("✓ Found %d rides", len(activities)))
			})
		}()
	})
	fetchBtn.Importance = widget.HighImportance

	uploadAllBtn.OnTapped = func() {
		uploadAllBtn.Disable()
		appendLog("🚀 Batch upload started...")
		go func() {
			for _, act := range currentActivities {
				if !tracker.IsSynced(act.ID) {
					uploadActivity(act, nil)
					time.Sleep(1 * time.Second)
				}
			}
		}()
	}

	// --- UI LAYOUT ---
	top := container.NewVBox(
		widget.NewRichTextFromMarkdown("# 🚴 MyWhoosh ↔️ Garmin"),
		container.NewGridWithColumns(2,
			widget.NewCard("MyWhoosh", "", container.NewVBox(mwEmail, mwPass, mwStatus)),
			widget.NewCard("Garmin", "", container.NewVBox(garminEmail, garminPass, garminID, garminStatus)),
		),
		container.NewPadded(fetchBtn),
		container.NewCenter(automateCheck),
		widget.NewSeparator(),
	)

	split := container.NewVSplit(
		container.NewBorder(container.NewPadded(container.NewBorder(nil, nil, widget.NewLabel("Activities"), uploadAllBtn)), nil, nil, nil, activityScroll),
		container.NewBorder(container.NewPadded(widget.NewLabel("Log")), nil, nil, nil, logScroll),
	)
	split.SetOffset(0.6)

	w.SetContent(container.NewBorder(container.NewPadded(top), nil, nil, nil, split))
	w.Resize(fyne.NewSize(1000, 850))
	w.ShowAndRun()
}
