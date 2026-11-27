package replay_test

import (
	"github.com/Project-Helianthus/helianthus-canbus"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/replay"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/gree"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/growattlowvoltage"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

func commandFrames(into *[]replay.Frame, seen map[string]bool) error {
	if err := pcsCommands(into, seen); err != nil {
		return err
	}
	if err := linkCommands(into, seen); err != nil {
		return err
	}
	if err := greeCommands(into, seen); err != nil {
		return err
	}
	return lowVoltageCommands(into, seen)
}

func pcsCommands(into *[]replay.Frame, seen map[string]bool) error {
	clock, err := pcs.EncodeClockInput(pcs.ClockInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30})
	if err != nil {
		return err
	}
	addFrame(into, seen, "pcs", clock)
	discovery, err := pcs.EncodeDiscoveryControlInput(pcs.DiscoveryControlInput{Phase: 3, Parameter: 9})
	if err != nil {
		return err
	}
	addFrame(into, seen, "pcs", discovery)
	legacy := pcs.ContractLegacyExtended19
	when := pcs.ExtendedTimeInput{YearOffset: 26, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30}
	extended, err := pcs.EncodeExtendedTimeInput(legacy, when)
	if err != nil {
		return err
	}
	addFrame(into, seen, "pcs", extended)
	stack, err := pcs.EncodeStackStateInput(legacy, pcs.StackStateInput{CurrentCode: 0x0403, StateOfCharge: 0x0605})
	if err != nil {
		return err
	}
	addFrame(into, seen, "pcs", stack)
	quantity, err := pcs.EncodeSecondaryQuantityInput(legacy, pcs.SecondaryQuantityInput{Percentage: 0x0201, ByteFour: 0x09})
	if err != nil {
		return err
	}
	addFrame(into, seen, "pcs", quantity)
	update, err := pcs.EncodeUpdateCoordinationInput(legacy, pcs.UpdateCoordinationInput{Parameter: 4})
	if err != nil {
		return err
	}
	addFrame(into, seen, "pcs", update)
	return nil
}

func linkCommands(into *[]replay.Frame, seen map[string]bool) error {
	frame, err := link.EncodeClockInput(link.ClockInput{
		Destination: 245, Source: 1, Year: 2026, Month: 11, Day: 26, Hour: 13, Minute: 45, Second: 30, Extra: 7,
	})
	if err != nil {
		return err
	}
	addFrame(into, seen, "link", frame)
	return nil
}

func greeCommands(into *[]replay.Frame, seen map[string]bool) error {
	identity, err := canbus.NewInterfaceIdentity("can0", 1)
	if err != nil {
		return err
	}
	before := len(*into)
	seenType := map[uint8]bool{}
	for _, row := range gree.CommandRows() {
		if err := addGreeRow(into, seen, identity, seenType, row); err != nil {
			return err
		}
	}
	if len(*into) == before {
		return errMissingPackage("gree")
	}
	return nil
}

func addGreeRow(into *[]replay.Frame, seen map[string]bool, identity canbus.InterfaceIdentity, seenType map[uint8]bool, row gree.CommandRow) error {
	switch {
	case row.ClockID != 0:
		addFamily(into, seen, "gree", row.ClockID, true, zeros(8))
	case row.Property != nil:
		frame, err := gree.EncodeProperty(*row.Property, 0, 0, 0)
		if err != nil {
			return nil
		}
		addFamily(into, seen, "gree", frame.Identifier, true, frame.Data)
	case row.Setting != nil:
		if seenType[row.Setting.Type] {
			return nil
		}
		frame, err := gree.EncodeSetting(canbusreg.GreeVRFAddressedSetting{
			Key:        canbusreg.GreeVRFRegisterKey{Interface: identity, Family: canbusreg.GreeVRFData, Unit: 0x20, Type: row.Setting.Type},
			Class:      canbusreg.GreeVRFIndoorUnit,
			Coordinate: row.Setting.Start,
			RawValue:   0,
		})
		if err != nil {
			return nil
		}
		seenType[row.Setting.Type] = true
		addFamily(into, seen, "gree", frame.Identifier, true, frame.Data)
	}
	return nil
}

func lowVoltageCommands(into *[]replay.Frame, seen map[string]bool) error {
	event, err := growattlowvoltage.EncodeClockEvent(growattlowvoltage.ClockEvent{
		FrequencyEnable: true, Year: 26, Month: 11, Day: 27, Hour: 14, Minute: 30, Second: 45,
	})
	if err != nil {
		return err
	}
	addFrame(into, seen, "growattlowvoltage", event)
	serial, err := growattlowvoltage.EncodeSerialQuery(1)
	if err != nil {
		return err
	}
	addFrame(into, seen, "growattlowvoltage", serial)
	history, err := growattlowvoltage.EncodeHistoryQuery(1)
	if err != nil {
		return err
	}
	addFrame(into, seen, "growattlowvoltage", history)
	heartbeat, err := growattlowvoltage.EncodeV104InverterHeartbeat(growattlowvoltage.V104Selection{
		Profile: growattlowvoltage.V104Profile, Role: growattlowvoltage.V104InverterRole,
	})
	if err != nil {
		return err
	}
	addFrame(into, seen, "growattlowvoltage", heartbeat)
	return nil
}
