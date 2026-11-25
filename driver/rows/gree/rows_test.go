package gree

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	reg "github.com/Project-Helianthus/helianthus-canbusreg"
	"os"
	"testing"
)

func TestFixedClockRowsHaveSeparateDomains(t *testing.T) {
	var vectors []struct {
		Identifier uint32
		Weekday    uint8
		Payload    string
	}
	data, err := os.ReadFile("testdata/clocks.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		arg := reg.GreeVRFTime{Year: 24, Month: 12, Day: 31, Hour: 23, Minute: 59, Second: 58, Weekday: v.Weekday}
		id := "time"
		oracle := reg.EncodeGreeVRFTimeCommand
		if v.Identifier == 0x04820084 {
			id = "clock"
			oracle = reg.EncodeGreeVRFClockCommand
		}
		want, ok := oracle(arg)
		var frames []reg.GreeVRFCommand
		record := func(f reg.GreeVRFCommand) error { frames = append(frames, f); return nil }
		err := SendClock(id, arg, record)
		if err != nil || !ok || len(frames) != 1 || frames[0].Identifier != v.Identifier || hex.EncodeToString(frames[0].Data) != v.Payload || want.Identifier != frames[0].Identifier || !bytes.Equal(want.Data, frames[0].Data) {
			t.Fatalf("%s: %+v, %v", id, frames, err)
		}
		frames = nil
		arg.Weekday = 100
		if id == "clock" {
			arg.Weekday = 7
		}
		if err := SendClock(id, arg, record); err == nil || len(frames) != 0 {
			t.Fatalf("%s accepted weekday %d: %+v", id, arg.Weekday, frames)
		}
	}
}
