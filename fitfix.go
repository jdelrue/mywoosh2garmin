package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
)

// FIT protocol invalid sentinel values per base type.
const (
	uint8Invalid  = uint8(0xFF)
	uint16Invalid = uint16(0xFFFF)
	sint8Invalid  = int8(0x7F)
)

const garminManufacturer = typedef.ManufacturerGarmin // 1

// spoofDevice is a Garmin device that uploaded activities pretend to be recorded on.
type spoofDevice struct {
	Name    string
	Product typedef.GarminProduct
	Serial  uint32
}

// spoofDevices lists the devices the user can choose from; the first is the default.
// Serial is left empty here and filled in from the app config.
var spoofDevices = []spoofDevice{
	{Name: "Fenix 6S Pro", Product: typedef.GarminProductFenix6s}, // 3288
	{Name: "Edge 840", Product: typedef.GarminProductEdge840},     // 4062
}

// randomSerial returns a serial number that is neither 0 nor the FIT invalid value 0xFFFFFFFF.
func randomSerial() uint32 {
	return rand.Uint32N(math.MaxUint32-1) + 1
}

func spoofDeviceNames() []string {
	names := make([]string, len(spoofDevices))
	for i, d := range spoofDevices {
		names[i] = d.Name
	}

	return names
}

// findSpoofDevice returns the device with the given name, or the default one.
func findSpoofDevice(name string) spoofDevice {
	for _, d := range spoofDevices {
		if d.Name == name {
			return d
		}
	}

	return spoofDevices[0]
}

// logFn can be overridden to redirect log output (e.g., to a GUI).
var logFn = func(format string, args ...any) {
	fmt.Printf(format, args...)
}

// fixFitFile reads a MyWhoosh FIT activity, fixes missing session averages,
// strips temperature from records, spoofs the device, and writes the result.
func fixFitFile(inputPath, outputPath string, device spoofDevice) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	lis := filedef.NewListener()
	defer lis.Close()

	dec := decoder.New(f,
		decoder.WithMesgListener(lis),
		decoder.WithBroadcastOnly(),
	)

	_, err = dec.Decode()
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	activity, ok := lis.File().(*filedef.Activity)
	if !ok {
		return fmt.Errorf("not an activity file (got %T)", lis.File())
	}

	// Collect metrics from records and strip temperature
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

	logFn("Records: %d | Power: %d | HR: %d | Cadence: %d samples\n",
		len(activity.Records), len(powers), len(heartRates), len(cadences))

	// Fix missing session averages
	for _, sess := range activity.Sessions {
		if shouldFixU16(sess.AvgPower) && len(powers) > 0 {
			sess.AvgPower = avgU16(powers)
			logFn("  → avg power:      %d W\n", sess.AvgPower)
		}
		if shouldFixU8(sess.AvgHeartRate) && len(heartRates) > 0 {
			sess.AvgHeartRate = avgU8(heartRates)
			logFn("  → avg heart rate: %d bpm\n", sess.AvgHeartRate)
		}
		if shouldFixU8(sess.AvgCadence) && len(cadences) > 0 {
			sess.AvgCadence = avgU8(cadences)
			logFn("  → avg cadence:    %d rpm\n", sess.AvgCadence)
		}
	}

	// Spoof device
	applySpoofDevice(activity, device)

	// Encode
	fit := activity.ToFIT(nil)

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}

	if err := encoder.New(out, encoder.WithProtocolVersion(proto.V2)).Encode(&fit); err != nil {
		_ = out.Close()

		return err
	}

	return out.Close()
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

// applySpoofDevice makes the activity look as if it was recorded on the given Garmin device,
// so Garmin Connect computes Training Effect and VO2max for it.
func applySpoofDevice(activity *filedef.Activity, device spoofDevice) {
	activity.FileId.Manufacturer = garminManufacturer
	activity.FileId.Product = device.Product.Uint16()
	activity.FileId.SerialNumber = device.Serial

	for _, di := range activity.DeviceInfos {
		di.Manufacturer = garminManufacturer
		di.Product = device.Product.Uint16()
		di.SerialNumber = device.Serial
	}

	logFn("  → device spoofed: Garmin %s (product %d)\n", device.Name, device.Product)
}
