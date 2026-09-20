# helianthus-canbusreg

Fail-closed CAN profile registry for Helianthus.

The caller-selected, receive-only [Growatt low-voltage BMS common
projection](https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/7acf59e37f511f0b0cc305c80b093a7da232edec/protocols/growatt/growatt-low-voltage-bms-can-common-projection-v1.md)
exposes only fields shared by the documented V1.04 and V1.08 layouts for one
explicit source interface. It retains raw evidence, never derives a protocol
revision from firmware, and does not alter the separately selected V1.04
profile.
