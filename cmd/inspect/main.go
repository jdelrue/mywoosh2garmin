// Inspect tool: dumps file_id and device_info messages from a FIT file.
package main

import (
	"fmt"
	"os"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/filedef"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
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
		panic(err)
	}

	activity, ok := lis.File().(*filedef.Activity)
	if !ok {
		fmt.Printf("Not an activity file: %T\n", lis.File())
		return
	}

	fmt.Println("=== file_id ===")
	fmt.Printf("  Manufacturer:  %d (%s)\n", activity.FileId.Manufacturer, activity.FileId.Manufacturer)
	fmt.Printf("  Product:       %d\n", activity.FileId.Product)
	fmt.Printf("  SerialNumber:  %d\n", activity.FileId.SerialNumber)
	fmt.Printf("  TimeCreated:   %v\n", activity.FileId.TimeCreated)

	fmt.Printf("\n=== device_info (%d messages) ===\n", len(activity.DeviceInfos))
	for i, di := range activity.DeviceInfos {
		fmt.Printf("[%d] Manufacturer: %d (%s), Product: %d, Serial: %d, DeviceIndex: %d\n",
			i, di.Manufacturer, di.Manufacturer, di.Product, di.SerialNumber, di.DeviceIndex)
	}

	fmt.Printf("\n=== sessions (%d) ===\n", len(activity.Sessions))
	for i, s := range activity.Sessions {
		fmt.Printf("[%d] AvgPower: %d, AvgHR: %d, AvgCadence: %d, Sport: %s\n",
			i, s.AvgPower, s.AvgHeartRate, s.AvgCadence, s.Sport)
	}
}
