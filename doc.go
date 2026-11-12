// Package canbusreg classifies caller-supplied CAN evidence using explicitly
// selected product-family profiles and bounded receive-only replay providers.
// Transport I/O and interface configuration belong to the transport owner.
//
// Native contracts are published at
// https://github.com/Project-Helianthus/helianthus-docs-canbus.
// Raw evidence, candidate protocol grades and physical qualification are distinct.
// Proven, Hypothesis and Unknown describe contract evidence, not device approval.
// Admission fails closed; partial failures do not renew last-known-good fields.
// Stateful types document their own synchronization and lifecycle contracts.
package canbusreg
