package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

const (
	uint8Invalid  = uint8(0xFF)
	uint16Invalid = uint16(0xFFFF)
	sint8Invalid  = int8(0x7F)
)

const garminManufacturer = typedef.ManufacturerGarmin // 1

var logFn = func(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}

func findMyWhooshDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	switch runtime.GOOS {
	case "darwin":
		p := filepath.Join(home, "Library", "Containers", "com.whoosh.whooshgame", "Data", "Library", "Application Support", "Epic", "MyWhoosh", "Content", "Data")
		if isDir(p) {
			return p, nil
		}
		return "", fmt.Errorf("not found: %s", p)
	case "windows":
		base := filepath.Join(home, "AppData", "Local", "Packages")
		entries, err := os.ReadDir(base)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "MyWhooshTechnologyService.") {
				p := filepath.Join(base, e.Name(), "LocalCache", "Local", "MyWhoosh", "Content", "Data")
				if isDir(p) {
					return p, nil
				}
			}
		}
		return "", fmt.Errorf("MyWhoosh not found in %s", base)
	default:
		return "", fmt.Errorf("no auto-detection for %s", runtime.GOOS)
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func findMostRecentFitFile(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "MyNewActivity-*.fit"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no MyNewActivity-*.fit files in %s", dir)
	}

	re := regexp.MustCompile(`\d+`)
	sort.Slice(matches, func(i, j int) bool {
		vi := re.FindAllString(filepath.Base(matches[i]), -1)
		vj := re.FindAllString(filepath.Base(matches[j]), -1)
		return cmpVersionParts(vi, vj) > 0
	})
	return matches[0], nil
}

func cmpVersionParts(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		ai, _ := strconv.Atoi(a[i])
		bi, _ := strconv.Atoi(b[i])
		if ai != bi {
			return ai - bi
		}
	}
	return len(a) - len(b)
}

func generateOutputFilename(inputPath string) string {
	name := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	ts := time.Now().Format("2006-01-02_150405")
	return fmt.Sprintf("%s_%s.fit", name, ts)
}

func fixFitFile(inputPath, outputPath string, unitIDStr string) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	lis := filedef.NewListener()
	defer lis.Close()

	dec := decoder.New(f, decoder.WithMesgListener(lis), decoder.WithBroadcastOnly())
	if _, err = dec.Decode(); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	activity, ok := lis.File().(*filedef.Activity)
	if !ok {
		return fmt.Errorf("not an activity file (got %T)", lis.File())
	}

	activity.FileId.TimeCreated = activity.FileId.TimeCreated.Add(1 * time.Second)
	activity.Activity.Timestamp = activity.Activity.Timestamp.Add(1 * time.Second)

	var powers []uint16
	var heartRates, cadences []uint8

	for _, rec := range activity.Records {
		if rec.Power != uint16Invalid {
			powers = append(powers, rec.Power)
		}
		if rec.HeartRate != uint8Invalid {
			heartRates = append(heartRates, rec.HeartRate)
		}
		if rec.Cadence != uint8Invalid {
			cadences = append(cadences, rec.Cadence)
		}
		rec.Temperature = sint8Invalid
	}

	logFn("Records: %d | Power: %d | HR: %d | Cadence: %d samples\n", len(activity.Records), len(powers), len(heartRates), len(cadences))

	for _, sess := range activity.Sessions {
		if shouldFixU16(sess.AvgPower) && len(powers) > 0 {
			sess.AvgPower = avgU16(powers)
		}
		if shouldFixU8(sess.AvgHeartRate) && len(heartRates) > 0 {
			sess.AvgHeartRate = avgU8(heartRates)
		}
		if shouldFixU8(sess.AvgCadence) && len(cadences) > 0 {
			sess.AvgCadence = avgU8(cadences)
		}
	}

	spoofDevice(activity, unitIDStr)

	fit := activity.ToFIT(nil)
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	return encoder.New(out, encoder.WithProtocolVersion(proto.V2)).Encode(&fit)
}

func shouldFixU16(v uint16) bool { return v == uint16Invalid || v == 0 }
func shouldFixU8(v uint8) bool   { return v == uint8Invalid || v == 0 }
func avgU16(vals []uint16) uint16 {
	var sum uint64
	for _, v := range vals {
		sum += uint64(v)
	}
	return uint16(sum / uint64(len(vals)))
}
func avgU8(vals []uint8) uint8 {
	var sum uint64
	for _, v := range vals {
		sum += uint64(v)
	}
	return uint8(sum / uint64(len(vals)))
}

func spoofDevice(activity *filedef.Activity, unitIDStr string) {
	// Parse the string from the GUI into a number
	unitID, err := strconv.ParseUint(unitIDStr, 10, 32)
	if err != nil || unitID == 0 {
		// Fallback to a reliable serial if they type text or leave it blank
		unitID = 3420897194
	}

	serialNum := uint32(unitID)
	productID := typedef.GarminProductEdge1040.Uint16()

	activity.FileId.Manufacturer = garminManufacturer
	activity.FileId.Product = productID
	activity.FileId.SerialNumber = serialNum

	for i := range activity.DeviceInfos {
		activity.DeviceInfos[i].Manufacturer = garminManufacturer
		activity.DeviceInfos[i].Product = productID
		activity.DeviceInfos[i].SerialNumber = serialNum
	}

	logFn("  → Device mapped to Unit ID: %d\n", serialNum)
}

func findUnsyncedFitFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.fit"))
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -30)
	var result []string
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.ModTime().Before(cutoff) || isSynced(path) {
			continue
		}
		result = append(result, path)
	}
	sort.Slice(result, func(i, j int) bool {
		ii, _ := os.Stat(result[i])
		jj, _ := os.Stat(result[j])
		return ii.ModTime().Before(jj.ModTime())
	})
	return result, nil
}

func isSynced(fitPath string) bool {
	_, err := os.Stat(fitPath + ".synced")
	return err == nil
}

func markSynced(fitPath string) error {
	return os.WriteFile(fitPath+".synced", []byte(time.Now().Format(time.RFC3339)), 0o644)
}
