// Package pcs carries the table rows of the Growatt low-voltage battery PCS CAN
// interface: the native rows of each field the standard PCS decoder emits, with
// the selection conditions under which the decoder emits it. The rows of this
// slice cover the standard frames 0x311 to 0x314 (standard.go): the charge and
// discharge limits and the status words of 0x311, the status and padding bytes
// of 0x312, the voltage, current, temperature, state of charge and state of
// health of 0x313, and the capacity, cell spread and cycle count of 0x314.
//
// The package has no bindings yet: each native row is published with its raw
// word, its exact affine scale and its native unit. The 313 voltage and
// temperature keep their legacy and release-11 meanings as distinct rows, so a
// row never carries the meaning of the other contract. A row whose decoder gate
// is not met is withheld with its raw word kept (gateWithheld), never replaced
// by a value.
//
// table.go holds the selection vocabulary (Qualifiers), the descriptors, the
// gate hooks and the evaluation helpers. The rows are evaluated with the
// pinned roweval package, and rows_test.go holds the harness that compares
// them with the frozen decoder on identical bytes and selection.
package pcs
