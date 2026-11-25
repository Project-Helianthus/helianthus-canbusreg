// Package bms carries the table rows of the Growatt low-voltage BMS CAN
// profiles: the native rows of each field, and the bindings of the fields the
// pinned SemReg storage pack defines to that pack. The profiles covered here
// are growatt.bms.low_voltage.can.v1_04 (frames 0x311 to 0x313 in this file).
// The common projection, the addressed monitor profile, the V1.08 profile, the
// frames 0x314 to 0x321 and the storage relations are not in this file.
package bms
