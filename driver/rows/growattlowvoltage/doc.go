// Package growattlowvoltage carries the native rows of the revision-specific
// Growatt low-voltage BMS CAN contracts that have a transmit path. This slice
// holds the V1.08 clock and event frame 0x211.
//
// The V1.08 rows are selectable only by an explicit V1.08 choice. A selection
// for the PCS profile, the common projection or a read-only mode cannot
// dispatch them, and neither choosing a selection nor starting a driver
// generates a frame: a frame is encoded and sent only when a caller triggers it.
//
// The contract is the public V1.08 page of the CAN protocol documentation,
// protocols/growatt/low-voltage-bms-can-v108.md, sections "Frame map" and
// "0x211 dispatch". It is distinct from the PCS clock input, whose bytes 0 and 7
// are unused.
package growattlowvoltage
