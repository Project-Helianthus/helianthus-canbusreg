package link

import "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"

// AllTable returns every native row of the LINK interface (module telemetry,
// fine temperature and capacity layouts, slot vectors, coordinator frames,
// identity announcements and diagnostics) with the hooks those rows name and
// every read binding of the package. A row carries no withdrawal rule and no
// storage-graph switch: a stale or absent source is left to the runtime, and
// every bound element is published.
//
// Rows that read one payload slice under different selections share an id, so
// the table is validated part by part, as the parts are evaluated.
func AllTable() roweval.Table[Selector] {
	all := roweval.Table[Selector]{Hooks: map[string]roweval.RowHook{}, Bindings: append(Bindings(), statusBindings()...)}
	for _, p := range tableParts() {
		all.Native = append(all.Native, p.Native...)
		for name, hook := range p.Hooks {
			all.Hooks[name] = hook
		}
	}
	return all
}

// tableParts returns the native tables of the package in a stable order.
func tableParts() []roweval.Table[Selector] {
	return []roweval.Table[Selector]{
		Table(), FineTable(), VectorTable(), AggregateTable(), IdentityTable(), DiagnosticsTable(),
	}
}
