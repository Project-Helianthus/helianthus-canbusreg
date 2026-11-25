// Package bms carries the table rows of the Growatt low-voltage BMS CAN
// profiles: the native rows of each field, and the bindings of the fields the
// pinned SemReg storage pack defines to that pack. The profiles covered are
// growatt.bms.low_voltage.can.v1_04 (v104.go, frames 0x311 to 0x321), the
// common field projection (common.go, frames 0x311 to 0x313, selected by its
// publisher scope), the V1.08 battery-management profile (v108.go, frames 0x311
// to 0x325, the frames the frozen V1.08 report decoder admits) and the addressed
// monitor profile (monitor.go, the live fields of the extended addressed
// families). Each table evaluates only its own profile's frames and rows.
//
// The package binds only the fields the signed EX03 patterns and the pinned
// SemReg row types express. The device limits, the power product, the protection,
// warning and interlock symbols, the operating-state symbols, the cell extrema
// and cell temperatures, the storage resource and relation rows and the priority
// scores of the bindings have no binding here; the package report lists each of
// them with its reason.
package bms
