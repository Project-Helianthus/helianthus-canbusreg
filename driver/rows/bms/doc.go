// Package bms carries the table rows of the Growatt low-voltage BMS CAN
// profiles: the native rows of each field, and the bindings of the fields the
// pinned SemReg storage pack defines to that pack. The profiles covered are
// growatt.bms.low_voltage.can.v1_04 (v104.go, frames 0x311 to 0x321), the
// common field projection (common.go, frames 0x311 to 0x313, selected by its
// publisher scope) and the V1.08 battery-management profile (v108.go, frames
// 0x311 to 0x313). The addressed monitor profile, the storage resource and
// relation rows and the priority scores of the bindings are not in this package
// yet.
package bms
