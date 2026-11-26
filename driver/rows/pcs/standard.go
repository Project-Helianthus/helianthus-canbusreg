package pcs

// rowSpec describes one native row of a standard PCS frame. The field is the
// key DecodePCSStandard emits; the layout names the selection under which a
// repeated field name has its own slice or meaning (empty when unique); the
// gate is the gate qualifier the decoder tests (empty when the field is raw);
// the when conditions are added to the bank and flavor pairing every row carries.
type rowSpec struct {
	frame         uint32
	layout, field string
	slice         signalSlice
	unit          string
	divisor       int64
	gate          string
	percentDomain bool
	when          map[string]string
}

// Slices of the standard frames. Offsets are bit offsets within the 8-byte
// payload; 16-bit words are big-endian except where the frame states otherwise.
func bigWord(at uint16) signalSlice { return signalSlice{offset: at, width: 16, order: bigEndian} }
func signedBigWord(at uint16) signalSlice {
	return signalSlice{offset: at, width: 16, order: bigEndian, signed: true}
}
func littleWord(at uint16) signalSlice {
	return signalSlice{offset: at, width: 16, order: littleEndian}
}
func byteAt(at uint16) signalSlice {
	return signalSlice{offset: at, width: 8, order: bitSlice}
}
func bitsAt(at, width uint16) signalSlice {
	return signalSlice{offset: at, width: width, order: bitSlice}
}

// when returns the pairing every standard row carries: the flavor and the bank
// of its selection, plus the extra conditions of the row.
func when(extra ...string) map[string]string {
	m := map[string]string{qFlavor: "pcs-standard", qBank: "ordinary"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

// rawRow is a native row without a gate: its raw word is the native value.
func rawRow(frame uint32, field string, slice signalSlice) rowSpec {
	return rowSpec{frame: frame, field: field, slice: slice, divisor: 1, when: when()}
}

// standardSpecs returns the row specifications of frames 311 to 314, in frame order.
func standardSpecs() []rowSpec {
	var specs []rowSpec
	specs = append(specs, limitSpecs()...)
	specs = append(specs, summarySpecs()...)
	specs = append(specs, measurementSpecs()...)
	specs = append(specs, capacitySpecs()...)
	return specs
}

// limitSpecs are the rows of frame 0x311. The charge and discharge words are
// swapped on the gateway aggregate; each layout has its own row. The voltage
// and the aggregation-path words are the decoder's local, measured values; the
// hint bits are the local hypotheses; the status word is raw. No row carries a
// SemReg meaning, and the 0x311 scale is not established by the page.
func limitSpecs() []rowSpec {
	const f = 0x311
	local := func(s rowSpec) rowSpec { s.gate, s.when = gateLocalMeasured, when(qGateway, "false"); return s }
	gateway := func(s rowSpec) rowSpec { s.gate, s.when = gateLocalMeasured, when(qGateway, "true"); return s }
	hint := func(field string, slice signalSlice) rowSpec {
		return rowSpec{frame: f, field: field, slice: slice, divisor: 1, gate: gateLocal, when: when()}
	}
	return []rowSpec{
		{frame: f, field: "voltage_limit", slice: bigWord(0), unit: "V", divisor: 10, gate: gateLocalMeasured, when: when()},
		local(rowSpec{frame: f, layout: "direct", field: "charge_limit", slice: bigWord(16), unit: "A", divisor: 1}),
		gateway(rowSpec{frame: f, layout: "gateway", field: "charge_limit", slice: bigWord(32), unit: "A", divisor: 1}),
		local(rowSpec{frame: f, layout: "direct", field: "discharge_limit", slice: bigWord(32), unit: "A", divisor: 1}),
		gateway(rowSpec{frame: f, layout: "gateway", field: "discharge_limit", slice: bigWord(16), unit: "A", divisor: 1}),
		rawRow(f, "status", bigWord(48)),
		hint("raw_state", bitsAt(56, 2)),
		hint("fault_hint", bitsAt(58, 1)),
		hint("sleep_hint", bitsAt(60, 1)),
		hint("discharge_hint", bitsAt(61, 1)),
		hint("charge_hint", bitsAt(62, 1)),
		hint("force_hint", bitsAt(50, 1)),
	}
}

// summarySpecs are the rows of frame 0x312: the two little-endian status words,
// the reported count and the three padding bytes, all raw.
func summarySpecs() []rowSpec {
	const f = 0x312
	return []rowSpec{
		rawRow(f, "status_a", littleWord(0)),
		rawRow(f, "status_b", littleWord(16)),
		rawRow(f, "reported_count", byteAt(32)),
		rawRow(f, "padding_5", byteAt(40)),
		rawRow(f, "padding_6", byteAt(48)),
		rawRow(f, "padding_7", byteAt(56)),
	}
}

// measurementSpecs are the rows of frame 0x313. The voltage and the temperature
// have a legacy meaning (QAAA0006: pack voltage and coded minimum temperature)
// and a release-11 meaning (output-port voltage and count-aware fine average
// temperature); each meaning is its own row. The soh_high_bit is distinct too:
// its meaning differs between the two contracts.
func measurementSpecs() []rowSpec {
	const f = 0x313
	legacy := when(qRelease11, "false")
	release11 := when(qRelease11, "true")
	return []rowSpec{
		{frame: f, layout: "legacy", field: "voltage", slice: bigWord(0), unit: "V", divisor: 100, gate: gateMeasured, when: legacy},
		{frame: f, layout: "release11", field: "voltage", slice: bigWord(0), unit: "V", divisor: 100, gate: gateMeasured, when: release11},
		{frame: f, field: "current", slice: signedBigWord(16), unit: "A", divisor: 10, gate: gateMeasured, when: when()},
		{frame: f, layout: "legacy", field: "temperature", slice: signedBigWord(32), unit: "C", divisor: 10, gate: gateTemperature, when: legacy},
		{frame: f, layout: "release11", field: "temperature", slice: signedBigWord(32), unit: "C", divisor: 10, gate: gateTemperature, when: release11},
		{frame: f, field: "soc", slice: byteAt(48), unit: "percent", divisor: 1, gate: gateMeasured, percentDomain: true, when: when()},
		{frame: f, field: "soh", slice: bitsAt(56, 7), unit: "percent", divisor: 1, gate: gateMeasured, percentDomain: true, when: when()},
		rawRow(f, "soh_status", byteAt(56)),
		{frame: f, layout: "legacy", field: "soh_high_bit", slice: bitsAt(63, 1), divisor: 1, gate: gateMeasured, when: legacy},
		{frame: f, layout: "release11", field: "soh_high_bit", slice: bitsAt(63, 1), divisor: 1, gate: gateMeasured, when: release11},
	}
}

// capacitySpecs are the rows of frame 0x314: the remaining and full capacity in
// ampere-hours at two decimals, the cell spread in millivolts and the whole
// cycle count. Each is gated by the measurement assertion.
func capacitySpecs() []rowSpec {
	const f = 0x314
	return []rowSpec{
		{frame: f, field: "remaining_capacity", slice: bigWord(0), unit: "Ah", divisor: 100, gate: gateMeasured, when: when()},
		{frame: f, field: "full_capacity", slice: bigWord(16), unit: "Ah", divisor: 100, gate: gateMeasured, when: when()},
		{frame: f, field: "spread", slice: bigWord(32), unit: "mV", divisor: 1, gate: gateMeasured, when: when()},
		{frame: f, field: "cycles", slice: bigWord(48), unit: "cycles", divisor: 1, gate: gateMeasured, when: when()},
	}
}
