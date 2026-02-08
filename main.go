package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"mywhoosh2garmin/garmin"
)

// ---------------------------------------------------------------------------
// App config (persisted to ~/.mywhoosh2garmin/config.json)
// ---------------------------------------------------------------------------

type appConfig struct {
	MyWhooshDir string `json:"mywhoosh_dir"`
	Email       string `json:"email"`
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
// GUI
// ---------------------------------------------------------------------------

func main() {
	a := app.New()
	w := a.NewWindow("MyWhoosh2Garmin")
	w.Resize(fyne.NewSize(620, 520))

	cfg := loadAppConfig()

	// --- Widgets ---

	dirEntry := widget.NewEntry()
	dirEntry.SetPlaceHolder("Path to MyWhoosh FIT file directory")
	if cfg.MyWhooshDir != "" {
		dirEntry.SetText(cfg.MyWhooshDir)
	}

	emailEntry := widget.NewEntry()
	emailEntry.SetPlaceHolder("Garmin email")
	if cfg.Email != "" {
		emailEntry.SetText(cfg.Email)
	}

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Garmin password (only needed first time)")

	logLabel := widget.NewLabel("")
	logLabel.Wrapping = fyne.TextWrapWord
	logScroll := container.NewVScroll(logLabel)
	logScroll.SetMinSize(fyne.NewSize(0, 220))

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

	// Wire FIT processing logs into the GUI
	logFn = func(format string, args ...interface{}) {
		appendLog(fmt.Sprintf(format, args...))
	}

	// --- Find MyWhoosh Dir button ---
	findBtn := widget.NewButton("🔍  Find MyWhoosh Dir", func() {
		dir, err := findMyWhooshDir()
		if err != nil {
			appendLog("Auto-detect not available: " + err.Error())
			appendLog("Pick the directory manually…")
			dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
				if err != nil || uri == nil {
					return
				}
				dirEntry.SetText(uri.Path())
				cfg.MyWhooshDir = uri.Path()
				saveAppConfig(cfg)
				appendLog("✓ Directory set: " + uri.Path())
			}, w)
			return
		}
		dirEntry.SetText(dir)
		cfg.MyWhooshDir = dir
		saveAppConfig(cfg)
		appendLog("✓ Found MyWhoosh dir: " + dir)
	})

	// --- Sync button ---
	var syncing bool
	syncBtn := widget.NewButton("🔄  Sync to Garmin", nil)
	syncBtn.Importance = widget.HighImportance

	syncBtn.OnTapped = func() {
		if syncing {
			return
		}
		syncing = true
		syncBtn.Disable()

		go func() {
			defer func() {
				syncing = false
				syncBtn.Enable()
			}()

			// Persist config
			cfg.MyWhooshDir = dirEntry.Text
			cfg.Email = emailEntry.Text
			saveAppConfig(cfg)

			if cfg.MyWhooshDir == "" {
				appendLog("❌ Set MyWhoosh directory first")
				return
			}

			// 1. Find unsynced FIT files
			appendLog("Scanning for unsynced activities (last 30 days)…")
			files, err := findUnsyncedFitFiles(cfg.MyWhooshDir)
			if err != nil {
				appendLog("❌ Scan failed: " + err.Error())
				return
			}
			if len(files) == 0 {
				appendLog("✓ Everything is already synced!")
				return
			}
			appendLog(fmt.Sprintf("Found %d unsynced activity file(s)", len(files)))

			// 2. Authenticate to Garmin
			tokenDir := appConfigDir()
			client := garmin.NewClient(tokenDir)

			if err := client.Resume(); err == nil {
				appendLog("Garmin session resumed")
			} else {
				email := emailEntry.Text
				password := passwordEntry.Text
				if email == "" || password == "" {
					appendLog("❌ Enter Garmin email & password for first login")
					return
				}
				appendLog("Logging in to Garmin Connect…")
				if err := client.Login(email, password); err != nil {
					appendLog("❌ Login failed: " + err.Error())
					return
				}
				appendLog("✓ Logged in to Garmin Connect")
			}

			// 3. Process + upload each file
			tmpDir := os.TempDir()
			success, skipped := 0, 0

			for i, fitFile := range files {
				name := filepath.Base(fitFile)
				appendLog(fmt.Sprintf("\n[%d/%d] %s", i+1, len(files), name))

				outPath := filepath.Join(tmpDir, generateOutputFilename(fitFile))

				if err := fixFitFile(fitFile, outPath); err != nil {
					appendLog("  ❌ Processing failed: " + err.Error())
					skipped++
					continue
				}

				appendLog("  Uploading…")
				if err := client.UploadFIT(outPath); err != nil {
					if strings.Contains(err.Error(), "duplicate") {
						markSynced(fitFile)
						appendLog("  ⚠ Already on Garmin (marked synced)")
					} else {
						appendLog("  ❌ Upload failed: " + err.Error())
						skipped++
					}
					os.Remove(outPath)
					continue
				}

				markSynced(fitFile)
				os.Remove(outPath)
				success++
				appendLog("  ✓ Uploaded")
			}

			appendLog(fmt.Sprintf("\n✓ Sync complete — %d uploaded, %d skipped", success, skipped))
		}()
	}

	// --- Layout ---
	title := widget.NewRichTextFromMarkdown("## MyWhoosh → Garmin")

	form := container.NewVBox(
		title,
		widget.NewSeparator(),
		widget.NewLabel("MyWhoosh Directory"),
		dirEntry,
		findBtn,
		widget.NewSeparator(),
		widget.NewLabel("Garmin Connect"),
		emailEntry,
		passwordEntry,
		syncBtn,
		widget.NewSeparator(),
	)

	content := container.NewBorder(form, nil, nil, nil, logScroll)
	w.SetContent(content)
	w.ShowAndRun()
}
