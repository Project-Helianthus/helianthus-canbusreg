package growattlowvoltage

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

type heartbeatOperand struct {
	Revision string `json:"revision"`
	Role     string `json:"role"`
	Frame    struct {
		Identifier   string `json:"identifier"`
		Format       string `json:"format"`
		Kind         string `json:"kind"`
		DLC          int    `json:"dlc"`
		BytesDecimal []int  `json:"bytes_decimal"`
		Payload      string `json:"payload"`
	} `json:"frame"`
}

func loadHeartbeatOperand(t *testing.T) heartbeatOperand {
	t.Helper()
	raw, err := os.ReadFile("testdata/v104_inverter_heartbeat.json")
	if err != nil {
		t.Fatal(err)
	}
	var o heartbeatOperand
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

var v104Inverter = V104Selection{Profile: V104Profile, Role: V104InverterRole}

func TestV104InverterHeartbeatFrame(t *testing.T) {
	o := loadHeartbeatOperand(t)
	if o.Revision != V104Profile || o.Role != V104InverterRole {
		t.Fatalf("operand bound to %s/%s", o.Revision, o.Role)
	}
	frame, err := EncodeV104InverterHeartbeat(v104Inverter)
	if err != nil {
		t.Fatal(err)
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(o.Frame.Identifier, "0x"), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(o.Frame.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range o.Frame.BytesDecimal {
		if int(want[i]) != d {
			t.Fatalf("operand byte %d: hex %x, decimal %d", i, want[i], d)
		}
	}
	if o.Frame.Format != "standard" || o.Frame.Kind != "data" {
		t.Fatalf("operand format %s kind %s", o.Frame.Format, o.Frame.Kind)
	}
	if frame.ID().Value() != uint32(id) || frame.ID().Extended() || int(frame.DLC()) != o.Frame.DLC || int(frame.RawDLC()) != o.Frame.DLC || !bytes.Equal(frame.Data(), want) {
		t.Errorf("got id %#x ext=%v dlc %d data %x, want standard %s dlc %d %x", frame.ID().Value(), frame.ID().Extended(), frame.DLC(), frame.Data(), o.Frame.Identifier, o.Frame.DLC, want)
	}
	if got := hex.EncodeToString(frame.Data()); got != "0b16212c37424d58" {
		t.Errorf("literal payload %s", got)
	}
}

func TestV104HeartbeatRefusesWrongRoleOrRevision(t *testing.T) {
	for name, sel := range map[string]V104Selection{
		"wrong role":      {Profile: V104Profile, Role: "battery"},
		"empty role":      {Profile: V104Profile},
		"v108 revision":   {Profile: Profile, Role: V104InverterRole},
		"empty selection": {},
	} {
		if _, err := EncodeV104InverterHeartbeat(sel); err != ErrNotV104Inverter {
			t.Errorf("%s: got %v, want ErrNotV104Inverter", name, err)
		}
	}
}

func TestV104HeartbeatNeedsNoIncomingTuple(t *testing.T) {
	first, err := EncodeV104InverterHeartbeat(v104Inverter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeV104InverterHeartbeat(v104Inverter)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Data(), second.Data()) || first.ID() != second.ID() {
		t.Errorf("repeated invocation differs: %x vs %x", first.Data(), second.Data())
	}
}
