package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

// Record values set by newTestActivity.
const (
	testPower     = 200
	testHeartRate = 150
	testCadence   = 90
)

// newTestActivity returns a MyWhoosh-like activity with:
//   - records that have power, HR, cadence, and temperature set
//   - a session with NO average power, HR, or cadence (simulating the MyWhoosh bug)
func newTestActivity() *filedef.Activity {
	now := time.Now()
	activity := filedef.NewActivity()

	activity.FileId.
		SetType(typedef.FileActivity).
		SetTimeCreated(now).
		SetManufacturer(typedef.ManufacturerDevelopment).
		SetProduct(0)

	for i := range 10 {
		rec := mesgdef.NewRecord(nil).
			SetTimestamp(now.Add(time.Duration(i) * time.Second)).
			SetPower(testPower).
			SetHeartRate(testHeartRate).
			SetCadence(testCadence).
			SetTemperature(25)
		activity.Records = append(activity.Records, rec)
	}

	activity.Laps = append(activity.Laps,
		mesgdef.NewLap(nil).
			SetTimestamp(now.Add(10*time.Second)).
			SetStartTime(now),
	)

	activity.Sessions = append(activity.Sessions,
		mesgdef.NewSession(nil).
			SetTimestamp(now.Add(10*time.Second)).
			SetStartTime(now).
			SetSport(typedef.SportCycling),
	)

	activity.Activity = mesgdef.NewActivity(nil).
		SetTimestamp(now.Add(10 * time.Second)).
		SetType(typedef.ActivityManual).
		SetNumSessions(1)

	return activity
}

func writeTestFit(t *testing.T, path string, fit *proto.FIT) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := encoder.New(f).Encode(fit); err != nil {
		t.Fatal(err)
	}
}

func readTestActivity(t *testing.T, path string) *filedef.Activity {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	lis := filedef.NewListener()
	defer lis.Close()

	if _, err := decoder.New(f, decoder.WithMesgListener(lis), decoder.WithBroadcastOnly()).Decode(); err != nil {
		t.Fatalf("decode fixed file: %v", err)
	}

	result, ok := lis.File().(*filedef.Activity)
	if !ok {
		t.Fatalf("expected activity file, got %T", lis.File())
	}

	return result
}

// fixTestActivity writes the activity to a FIT file, runs fixFitFile on it and returns the decoded result.
func fixTestActivity(t *testing.T, activity *filedef.Activity, device spoofDevice) *filedef.Activity {
	t.Helper()

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "MyNewActivity-3.8.5.fit")
	outputPath := filepath.Join(tmpDir, "MyNewActivity-3.8.5_fixed.fit")

	fit := activity.ToFIT(nil)
	writeTestFit(t, inputPath, &fit)

	if err := fixFitFile(inputPath, outputPath, device); err != nil {
		t.Fatalf("fixFitFile failed: %v", err)
	}

	return readTestActivity(t, outputPath)
}

func TestFixFitFile(t *testing.T) {
	t.Parallel()

	device := findSpoofDevice("Edge 840")
	device.Serial = randomSerial()

	result := fixTestActivity(t, newTestActivity(), device)

	for i, rec := range result.Records {
		if rec.Temperature != sint8Invalid {
			t.Errorf("record %d: temperature not removed (got %d)", i, rec.Temperature)
		}
	}

	if result.FileId.Product != device.Product.Uint16() ||
		result.FileId.SerialNumber != device.Serial {
		t.Errorf("file_id device: got product %d serial %d, want product %d serial %d",
			result.FileId.Product, result.FileId.SerialNumber, device.Product, device.Serial)
	}

	if len(result.Sessions) == 0 {
		t.Fatal("no sessions in output")
	}
	sess := result.Sessions[0]

	if sess.AvgPower != testPower {
		t.Errorf("avg power: got %d, want %d", sess.AvgPower, testPower)
	}
	if sess.AvgHeartRate != testHeartRate {
		t.Errorf("avg HR: got %d, want %d", sess.AvgHeartRate, testHeartRate)
	}
	if sess.AvgCadence != testCadence {
		t.Errorf("avg cadence: got %d, want %d", sess.AvgCadence, testCadence)
	}
}

func TestFixFitFileKeepsExistingAverages(t *testing.T) {
	t.Parallel()

	activity := newTestActivity()
	activity.Sessions[0].
		SetAvgPower(250).
		SetAvgHeartRate(140).
		SetAvgCadence(85)

	sess := fixTestActivity(t, activity, findSpoofDevice("")).Sessions[0]

	if sess.AvgPower != 250 || sess.AvgHeartRate != 140 || sess.AvgCadence != 85 {
		t.Errorf("averages overwritten: got power %d HR %d cadence %d, want 250 140 85",
			sess.AvgPower, sess.AvgHeartRate, sess.AvgCadence)
	}
}

func TestFixFitFileSkipsInvalidSamples(t *testing.T) {
	t.Parallel()

	activity := newTestActivity()
	// Half the records have no power, HR or cadence; they must not drag the averages down.
	for _, rec := range activity.Records[:5] {
		rec.Power = uint16Invalid
		rec.HeartRate = uint8Invalid
		rec.Cadence = uint8Invalid
	}
	for _, rec := range activity.Records[5:] {
		rec.SetPower(100).SetHeartRate(120).SetCadence(80)
	}

	sess := fixTestActivity(t, activity, findSpoofDevice("")).Sessions[0]

	if sess.AvgPower != 100 || sess.AvgHeartRate != 120 || sess.AvgCadence != 80 {
		t.Errorf("got power %d HR %d cadence %d, want 100 120 80",
			sess.AvgPower, sess.AvgHeartRate, sess.AvgCadence)
	}
}

func TestFixFitFileNoSamples(t *testing.T) {
	t.Parallel()

	activity := newTestActivity()
	for _, rec := range activity.Records {
		rec.Power = uint16Invalid
		rec.HeartRate = uint8Invalid
		rec.Cadence = uint8Invalid
	}

	sess := fixTestActivity(t, activity, findSpoofDevice("")).Sessions[0]

	if sess.AvgPower != uint16Invalid || sess.AvgHeartRate != uint8Invalid || sess.AvgCadence != uint8Invalid {
		t.Errorf("averages set without samples: got power %d HR %d cadence %d",
			sess.AvgPower, sess.AvgHeartRate, sess.AvgCadence)
	}
}

func TestFixFitFileSpoofsDeviceInfos(t *testing.T) {
	t.Parallel()

	activity := newTestActivity()
	activity.DeviceInfos = append(activity.DeviceInfos,
		mesgdef.NewDeviceInfo(nil).
			SetTimestamp(time.Now()).
			SetManufacturer(typedef.ManufacturerDevelopment).
			SetProduct(0).
			SetSerialNumber(1),
	)
	device := findSpoofDevice("Fenix 6S Pro")
	device.Serial = 12345

	result := fixTestActivity(t, activity, device)

	if len(result.DeviceInfos) != 1 {
		t.Fatalf("device infos: got %d, want 1", len(result.DeviceInfos))
	}
	di := result.DeviceInfos[0]
	if di.Manufacturer != garminManufacturer || di.Product != device.Product.Uint16() || di.SerialNumber != 12345 {
		t.Errorf("device info: got manufacturer %d product %d serial %d, want %d %d 12345",
			di.Manufacturer, di.Product, di.SerialNumber, garminManufacturer, device.Product)
	}
}

func TestFixFitFileErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "out.fit")

	if err := fixFitFile(filepath.Join(tmpDir, "missing.fit"), outputPath, findSpoofDevice("")); err == nil {
		t.Error("missing input: got nil error")
	}

	coursePath := filepath.Join(tmpDir, "course.fit")
	course := filedef.NewCourse()
	course.FileId.SetTimeCreated(time.Now())
	fit := course.ToFIT(nil)
	writeTestFit(t, coursePath, &fit)

	err := fixFitFile(coursePath, outputPath, findSpoofDevice(""))
	if err == nil || !strings.Contains(err.Error(), "not an activity") {
		t.Errorf("course file: got %v, want a \"not an activity\" error", err)
	}
}

func TestAvg(t *testing.T) {
	t.Parallel()

	// The sum of these exceeds the uint8/uint16 range.
	if got := avgU8([]uint8{250, 250, 250, 252}); got != 250 {
		t.Errorf("avgU8: got %d, want 250", got)
	}
	if got := avgU16([]uint16{65000, 65000, 65003}); got != 65001 {
		t.Errorf("avgU16: got %d, want 65001", got)
	}
	// Integer division truncates.
	if got := avgU8([]uint8{1, 2}); got != 1 {
		t.Errorf("avgU8 truncation: got %d, want 1", got)
	}
}

func TestShouldFix(t *testing.T) {
	t.Parallel()

	for in, want := range map[uint8]bool{0: true, uint8Invalid: true, 90: false} {
		if got := shouldFixU8(in); got != want {
			t.Errorf("shouldFixU8(%d) = %t, want %t", in, got, want)
		}
	}
	for in, want := range map[uint16]bool{0: true, uint16Invalid: true, 200: false} {
		if got := shouldFixU16(in); got != want {
			t.Errorf("shouldFixU16(%d) = %t, want %t", in, got, want)
		}
	}
}

func TestFindSpoofDevice(t *testing.T) {
	t.Parallel()

	if got := findSpoofDevice("Edge 840").Product; got != typedef.GarminProductEdge840 {
		t.Errorf("Edge 840: got product %d", got)
	}
	for _, name := range []string{"", "unknown"} {
		if got := findSpoofDevice(name); got != spoofDevices[0] {
			t.Errorf("%q: got %+v, want default %+v", name, got, spoofDevices[0])
		}
	}
}

func TestSpoofDeviceNames(t *testing.T) {
	t.Parallel()

	want := []string{"Fenix 6S Pro", "Edge 840"}
	if got := spoofDeviceNames(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSyncedTracker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	st := newSyncedTracker(dir)
	if st.IsSynced("a1") {
		t.Fatal("new tracker: a1 synced")
	}
	st.MarkSynced("a1")
	if !st.IsSynced("a1") {
		t.Fatal("a1 not synced after MarkSynced")
	}

	reloaded := newSyncedTracker(dir)
	if !reloaded.IsSynced("a1") || reloaded.IsSynced("a2") {
		t.Error("reloaded tracker: want only a1 synced")
	}
}

func TestSyncedTrackerBrokenFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "synced.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := newSyncedTracker(dir)
	if st.IsSynced("a1") {
		t.Error("broken file: a1 synced")
	}
	st.MarkSynced("a1")
	if !newSyncedTracker(dir).IsSynced("a1") {
		t.Error("broken file not overwritten by MarkSynced")
	}
}
