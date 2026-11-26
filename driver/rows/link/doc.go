// Package link carries the table rows of the Growatt low-voltage battery LINK
// CAN interface: one native row for each field the module telemetry decoder
// emits for the frames 1800 to 1808, with the selection conditions under which
// the decoder emits it.
//
// A LINK frame is an extended identifier 18kkDDSS with DLC 8 and raw DLC 8,
// where kk is the frame family, DD the destination address and SS the source
// address. The family is the row's selector; the destination and the source
// are selection conditions (the destination F4 and the module source address),
// so a row applies to the frames of its family on that route and to no other.
//
// A row id is growatt.bms.link.<field>. A field name that more than one LINK
// frame family emits takes the family as qualifier, growatt.bms.link.<frame>.<field>,
// so that independent native points never share an id. The decoder's field name
// stays the last segment of the id.
//
// A row whose value has a native unit under a selection (the local release-11
// voltage, current and temperature scales, the 0.1 Ah full-charge capacity of
// the QAAA0006 selection, the fine state of charge of the release-11 path) has
// a variant that carries the unit and the exact affine scale, and a variant
// that keeps the raw word unitless under every other selection. The raw word is
// kept in both. A field the decoder refuses (a non-ASCII identity, a
// configuration byte that is not the constant 1 under QAAA0006) is withheld with
// its raw word kept and the disposition invalid; a case mismatch of the
// 1802 identity keeps the decoder's conflict status with the same raw words.
//
// The package has no bindings and no command rows yet. The vectors of the other
// families, the diagnostics and the identity announcements are added by the later
// slices of the package.
package link
