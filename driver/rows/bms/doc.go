// Package bms carries the table rows of the Growatt low-voltage BMS CAN
// profiles: the native rows of each field, and the bindings of the fields the
// pinned SemReg storage pack defines to that pack. The profile covered here is
// growatt.bms.low_voltage.can.v1_04, frames 0x311 to 0x321 (v104.go). The
// common projection, the addressed monitor profile, the V1.08 profile and the
// storage resource and relation rows are not in this package yet.
package bms
