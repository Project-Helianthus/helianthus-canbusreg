package driver_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus/receiver"
	semhost "github.com/Project-Helianthus/helianthus-semreg/semreg/host"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/graph"

	"github.com/Project-Helianthus/helianthus-canbusreg/driver"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
)

func TestFactorySourceRefusesNil(t *testing.T) {
	_, err := driver.NewFactoryWithSource(nil)
	if err == nil || !strings.Contains(err.Error(), "frame source is required") {
		t.Fatal(err)
	}
}

func TestRecordedSourceHostsProjectFacts(t *testing.T) {
	pcsPayload, err := hex.DecodeString("14BEFFF6FFF65563")
	if err != nil {
		t.Fatal(err)
	}
	linkPayload, err := hex.DecodeString("1000020355035507")
	if err != nil {
		t.Fatal(err)
	}
	pcsRaw, err := json.Marshal(driver.Options{
		Profile: driver.ProfilePCS, CacheAge: time.Second,
		PCS: &driver.PCSSelection{
			Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs",
			Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Group: "0", Slot: "1", Epoch: 4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	linkRaw, err := json.Marshal(driver.Options{
		Profile: driver.ProfileLINK, CacheAge: time.Second,
		Sources: []driver.LINKSourceSelection{{
			PCSFlavor: "pcs-standard", ProjectCode: "QAAa", Release: "9011", Choice: "release11",
			Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "02",
			RouteSet: true, Destination: 0xF4, Source: 2, Epoch: 7,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scripts := map[string]replay.Script{
		"can0": {Name: "can0", Interface: "can0", Index: 1, Frames: []replay.Frame{{ID: 0x313, Data: pcsPayload, Monotonic: time.Millisecond}}},
		"can1": {Name: "can1", Interface: "can1", Index: 2, Frames: []replay.Frame{{ID: 0x1807F402, Extended: true, Data: linkPayload, Monotonic: time.Millisecond}}},
	}
	factory, err := driver.NewFactoryWithSource(func(name string) (receiver.Receiver, error) {
		script, ok := scripts[name]
		if !ok {
			return nil, fmt.Errorf("no recorded frames for %s", name)
		}
		return replay.Open(script)
	})
	if err != nil {
		t.Fatal(err)
	}
	calendar := "UTC"
	site, err := semhost.Open(context.Background(), semhost.Config{
		InstallationKey: "installation.synthetic", BridgeID: "bridge.synthetic",
		StateDir: t.TempDir(), Clock: projectClock{}, Calendar: &calendar,
		Instances: []semhost.InstanceConfig{
			{Kind: driver.Kind, Instance: "pcs-host", Enabled: true, Transport: json.RawMessage(`{"interface":"can0"}`), Options: pcsRaw},
			{Kind: driver.Kind, Instance: "link-host", Enabled: true, Transport: json.RawMessage(`{"interface":"can1"}`), Options: linkRaw},
		},
	}, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = site.Close(context.Background()) })
	if err := site.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := site.WaitInitial(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []struct{ dimension, fact, unit, value string }{
		{"can/pcs-host/pack", "storage.state.soh", "unit.ratio", "99/100"},
		{"can/pcs-host/pack", "storage.pack.voltage", "unit.volt", "531/10"},
		{"can/pcs-host/pack", "storage.pack.current", "unit.ampere", "-1"},
		{"can/link-host/pack.02", "storage.state.soc", "unit.ratio", "853/1000"},
		{"can/pcs-host/pack", "storage.state.soc", "unit.ratio", "85/100"},
	}
	deadline := time.Now().Add(5 * time.Second)
	var lines []string
	var captureErr error
	for {
		lines, captureErr = projectLines(site)
		if captureErr == nil && projectFactsPresent(t, lines, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("host facts missing: %v\n%s", captureErr, strings.Join(lines, "\n"))
		}
		time.Sleep(time.Millisecond)
	}
}

// hostAsset is the asset the host opens for one instance: the digest of the
// driver kind and the instance key. Each capture names one asset.
func hostAsset(instance string) (semreg.AssetID, error) {
	raw, err := json.Marshal([2]string{string(driver.Kind), instance})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return semreg.AssetID("asset:instance:" + hex.EncodeToString(sum[:8])), nil
}

type projectClock struct{}

func (projectClock) Now() time.Time { return time.Unix(0, 0).UTC() }

func (projectClock) Sample() (uint64, error) { return 1, nil }

func (projectClock) Anchor(sample uint64) ([]byte, error) {
	return []byte(strconv.FormatUint(sample, 10)), nil
}

func (projectClock) Elapsed(anchor []byte, sample uint64) (uint64, error) {
	base, err := strconv.ParseUint(string(anchor), 10, 64)
	if err != nil {
		return 0, err
	}
	if sample < base {
		return 0, fmt.Errorf("backward sample")
	}
	return sample - base, nil
}

func projectLines(site *semhost.Site) ([]string, error) {
	var lines []string
	for _, instance := range []string{"pcs-host", "link-host"} {
		asset, err := hostAsset(instance)
		if err != nil {
			return nil, err
		}
		scope := semdriver.Filter{Assets: []semreg.AssetID{asset}}
		cut, err := site.Tree().SemanticInput(scope).Capture(context.Background(), semdriver.Filter{})
		if err != nil {
			return lines, err
		}
		lines = append(lines, factLines(cut)...)
	}
	sort.Strings(lines)
	return lines, nil
}

func factLines(cut semdriver.SemanticCut) []string {
	var lines []string
	for _, snap := range cut.Snapshots {
		for _, env := range snap.Facts {
			for _, cand := range env.Candidates {
				if cand.Value == nil || cand.Value.Quantity == nil || len(cand.Key.Dimensions) != 1 || cand.Key.Dimensions[0].Value.Text == nil {
					continue
				}
				q := cand.Value.Quantity
				lines = append(lines, fmt.Sprintf("%s %s %s %s %d", *cand.Key.Dimensions[0].Value.Text, cand.Key.FactID, q.Unit, q.Number.Coefficient, q.Number.Exponent10))
			}
		}
	}
	sort.Strings(lines)
	return lines
}

func projectFactsPresent(t *testing.T, lines []string, want []struct{ dimension, fact, unit, value string }) bool {
	t.Helper()
	for _, one := range want {
		ratio, ok := new(big.Rat).SetString(one.value)
		if !ok {
			t.Fatal(one.value)
		}
		found := false
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) != 5 || fields[0] != one.dimension || fields[1] != one.fact || fields[2] != one.unit {
				continue
			}
			exp, err := strconv.Atoi(fields[4])
			if err != nil {
				t.Fatal(err)
			}
			got, ok := new(big.Rat).SetString(fields[3] + "e" + strconv.Itoa(exp))
			if ok && got.Cmp(ratio) == 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestInstallationCutKeepsProjectPacks(t *testing.T) {
	pcsA, err := hex.DecodeString("14BEFFF6FFF65563")
	if err != nil {
		t.Fatal(err)
	}
	pcsB, err := hex.DecodeString("14BEFFF6FFF65063")
	if err != nil {
		t.Fatal(err)
	}
	linkA, err := hex.DecodeString("1000020355035507")
	if err != nil {
		t.Fatal(err)
	}
	linkB, err := hex.DecodeString("10000203BC025507")
	if err != nil {
		t.Fatal(err)
	}
	pcsRaw, err := json.Marshal(driver.Options{
		Profile: driver.ProfilePCS, CacheAge: time.Second,
		PCS: &driver.PCSSelection{
			Flavor: "pcs-standard", ProjectCode: "QAAA", Release: "0006", Direction: "bms-to-pcs",
			Scope: "local", Bank: "ordinary", Endpoint: "coordinator", Group: "0", Slot: "1", Epoch: 4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	linkRaw, err := json.Marshal(driver.Options{
		Profile: driver.ProfileLINK, CacheAge: time.Second,
		Sources: []driver.LINKSourceSelection{{
			PCSFlavor: "pcs-standard", ProjectCode: "QAAa", Release: "9011", Choice: "release11",
			Direction: "module-to-coordinator", Scope: "local", Bank: "ordinary", Endpoint: "02",
			RouteSet: true, Destination: 0xF4, Source: 2, Epoch: 7,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := []struct {
		name string
		id   uint32
		ext  bool
		data []byte
	}{
		{"can0", 0x313, false, pcsA},
		{"can1", 0x313, false, pcsB},
		{"can2", 0x1807F402, true, linkA},
		{"can3", 0x1807F402, true, linkB},
	}
	scripts := map[string]replay.Script{}
	for i, frame := range frames {
		scripts[frame.name] = replay.Script{
			Name: frame.name, Interface: frame.name, Index: i + 1,
			Frames: []replay.Frame{{ID: frame.id, Extended: frame.ext, Data: frame.data, Monotonic: time.Millisecond}},
		}
	}
	factory, err := driver.NewFactoryWithSource(func(name string) (receiver.Receiver, error) {
		script, ok := scripts[name]
		if !ok {
			return nil, fmt.Errorf("no recorded frames for %s", name)
		}
		return replay.Open(script)
	})
	if err != nil {
		t.Fatal(err)
	}
	calendar := "UTC"
	site, err := semhost.Open(context.Background(), semhost.Config{
		InstallationKey: "installation.synthetic", BridgeID: "bridge.synthetic",
		StateDir: t.TempDir(), Clock: projectClock{}, Calendar: &calendar,
		Instances: []semhost.InstanceConfig{
			{Kind: driver.Kind, Instance: "pcs-a", Enabled: true, Transport: json.RawMessage(`{"interface":"can0"}`), Options: pcsRaw},
			{Kind: driver.Kind, Instance: "pcs-b", Enabled: true, Transport: json.RawMessage(`{"interface":"can1"}`), Options: pcsRaw},
			{Kind: driver.Kind, Instance: "link-a", Enabled: true, Transport: json.RawMessage(`{"interface":"can2"}`), Options: linkRaw},
			{Kind: driver.Kind, Instance: "link-b", Enabled: true, Transport: json.RawMessage(`{"interface":"can3"}`), Options: linkRaw},
		},
	}, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = site.Close(context.Background()) })
	if err := site.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := site.WaitInitial(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []struct{ dimension, fact, unit, value string }{
		{"can/pcs-a/pack", "storage.state.soc", "unit.ratio", "85/100"},
		{"can/pcs-b/pack", "storage.state.soc", "unit.ratio", "4/5"},
		{"can/link-a/pack.02", "storage.state.soc", "unit.ratio", "853/1000"},
		{"can/link-b/pack.02", "storage.state.soc", "unit.ratio", "7/10"},
	}
	wantIDs := []string{"can/pcs-a/pack", "can/pcs-b/pack", "can/link-a/pack.02", "can/link-b/pack.02"}
	deadline := time.Now().Add(5 * time.Second)
	var lines []string
	var captureErr error
	var cut semdriver.SemanticCut
	for {
		cut, captureErr = site.Tree().SemanticInput(semdriver.Filter{}).Capture(context.Background(), semdriver.Filter{})
		if captureErr == nil {
			for _, row := range cut.Ref.NotIncluded {
				if row.Reason == graph.NotIncludedExcluded {
					t.Fatalf("excluded asset %s", row.AssetID)
				}
			}
			lines = factLines(cut)
			seen := map[string]bool{}
			for _, snap := range cut.Snapshots {
				for _, res := range snap.Resources {
					seen[string(res.ResourceID)] = true
				}
			}
			ready := projectFactsPresent(t, lines, want)
			for _, id := range wantIDs {
				if !seen[id] {
					ready = false
				}
			}
			if ready {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("installation cut missing packs: %v\n%s", captureErr, strings.Join(lines, "\n"))
		}
		time.Sleep(time.Millisecond)
	}
}
