# helianthus-canbusreg

Fail-closed CAN profile registry for Helianthus.

The caller-selected, receive-only [Growatt low-voltage BMS common
projection](https://github.com/Project-Helianthus/helianthus-docs-canbus/blob/main/protocols/growatt/growatt-low-voltage-bms-can-common-projection-v1.md)
exposes only fields shared by the documented V1.04 and V1.08 layouts for one
explicit source interface. It retains raw evidence, never derives a protocol
revision from firmware, and does not alter the separately selected V1.04
profile.

The bounded `O08GrowattProvider` wraps the existing caller-selected V1.04
qualification replay. `O08NewGrowattProvider(interfaceIdentity, O08RevisionV104)`
requires an exact interface name/index and an explicit revision; other revision
selections are rejected. It does not infer a revision from firmware or change
the common-field projection or native admission rules. The native contract
remains owned by [helianthus-docs-canbus](https://github.com/Project-Helianthus/helianthus-docs-canbus).

Inject an `O08ReceiveSource` implementing
`O08Receive(context.Context) (O08Observation, error)` and call
`O08Start(source, generation)`. Each observation supplies value-owned `Evidence`
and the same positive source generation. Generations must increase across
starts, and observation sequences must strictly increase within a generation
(the first sequence may be zero). Wrong-interface, wrong-generation and repeated
or older-sequence observations are discarded before replay and raw publication.
The provider never opens or configures an interface and exposes no send or probe
method. The source must return promptly on context cancellation. `O08Stop()`
fences publication, cancels and joins the receiver; replacement starts also join
the predecessor before creating a fresh replay. A source that ignores cancellation
can prevent stop or replacement from completing.

`O08Snapshot()` returns detached status, the latest accepted source-local raw
observation and at most one last accepted V1.04 tuple. Raw and projection
generations identify historical data across restarts. `stopped` means no active
source; `awaiting` means a complete tuple has not been accepted by the current
replay; `receiving` means the replay most recently accepted a tuple; `degraded`
means the source returned an error. These states are not freshness or physical
qualification claims. Source loss, partial or malformed input, stop and restart
preserve the last accepted tuple. A successor cannot combine its partial tuple
with predecessor replay state. Retention is bounded and in memory only; errors
are reported as degraded status without retaining source error text.

Proven: focused synthetic offline lifecycle behavior. Hypothesis: suitability
for production acquisition when composed with a transport owner. Unknown:
physical acceptance, Gree raw/candidate lifecycle, actual interface ownership
and gateway composition. These evidence labels are separate from V1.04 replay
qualification. This partial provider slice makes no physical support or release
closure claim; the API is not stable before Helianthus 1.0.
