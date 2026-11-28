package driver

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/Project-Helianthus/helianthus-canbus"
	semreg "github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	semdriver "github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/roweval"

	canbusreg "github.com/Project-Helianthus/helianthus-canbusreg"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/link"
	"github.com/Project-Helianthus/helianthus-canbusreg/driver/rows/pcs"
)

// selectionKeys are the When keys that name the configured selection. A key
// outside this set is a frame gate or a field disposition, not a membership test.
var selectionKeys = map[string]struct{}{
	"flavor": {}, "bank": {}, "contract-group": {}, "layout": {},
	"scope": {}, "direction": {}, "endpoint": {}, "group": {}, "slot": {},
	"destination": {}, "source": {}, "release-11": {}, "local": {}, "gateway": {},
	"local-units": {}, "group-two": {}, "group-eight": {}, "group-present": {},
	"fine-group": {}, "configuration-word": {},
}

func probeIdentity(name string) (canbus.InterfaceIdentity, error) {
	return canbus.NewInterfaceIdentity(name, 1)
}

func finishParse(iface string, o Options) (InstanceConfig, error) {
	switch o.Profile {
	case ProfilePCS:
		return finishPCS(iface, o)
	case ProfileLINK:
		return finishLINK(iface, o)
	}
	if o.CacheAge != 0 {
		return InstanceConfig{}, errors.New("options: cache_age applies only to the PCS and LINK profiles")
	}
	if o.PCS != nil {
		return InstanceConfig{}, errors.New("options: pcs applies only to the PCS profile")
	}
	if len(o.Sources) != 0 {
		return InstanceConfig{}, errors.New("options: sources applies only to the LINK profile")
	}
	return InstanceConfig{Interface: iface, Options: o}, nil
}

func requireCache(age time.Duration) error {
	if age <= 0 {
		return errors.New("options: cache_age is required")
	}
	if age >= time.Duration(math.MaxInt64) {
		return errors.New("options: cache_age cannot be retained")
	}
	return nil
}

func finishPCS(iface string, o Options) (InstanceConfig, error) {
	if err := requireCache(o.CacheAge); err != nil {
		return InstanceConfig{}, err
	}
	if len(o.Sources) != 0 {
		return InstanceConfig{}, errors.New("options: sources applies only to the LINK profile")
	}
	if o.PCS == nil {
		return InstanceConfig{}, errors.New("options: pcs is required")
	}
	id, err := probeIdentity(iface)
	if err != nil {
		return InstanceConfig{}, err
	}
	if _, err := NewPCSSelection(id, *o.PCS); err != nil {
		return InstanceConfig{}, fmt.Errorf("options: %w", err)
	}
	return InstanceConfig{Interface: iface, Options: o}, nil
}

func finishLINK(iface string, o Options) (InstanceConfig, error) {
	if err := requireCache(o.CacheAge); err != nil {
		return InstanceConfig{}, err
	}
	if o.PCS != nil {
		return InstanceConfig{}, errors.New("options: pcs applies only to the PCS profile")
	}
	if len(o.Sources) == 0 {
		return InstanceConfig{}, errors.New("options: sources is required")
	}
	id, err := probeIdentity(iface)
	if err != nil {
		return InstanceConfig{}, err
	}
	if _, err := NewLINKSelections(id, o.Sources); err != nil {
		return InstanceConfig{}, fmt.Errorf("options: %w", err)
	}
	return InstanceConfig{Interface: iface, Options: o}, nil
}

func projectCommandIDs(profile Profile) []string {
	switch profile {
	case ProfilePCS:
		ids := make([]string, 0, len(pcs.Commands())+len(pcs.ExtendedCommands()))
		for _, row := range pcs.Commands() {
			ids = append(ids, row.ID)
		}
		for _, row := range pcs.ExtendedCommands() {
			ids = append(ids, row.ID)
		}
		return ids
	case ProfileLINK:
		ids := make([]string, 0, len(link.Commands()))
		for _, row := range link.Commands() {
			ids = append(ids, row.ID)
		}
		return ids
	default:
		return nil
	}
}

func (i *instance) projectDecoder(identity canbus.InterfaceIdentity) (decoder, error) {
	switch i.config.Options.Profile {
	case ProfilePCS:
		if i.config.Options.PCS == nil {
			return nil, errors.New("options: pcs is required")
		}
		if _, err := NewPCSSelection(identity, *i.config.Options.PCS); err != nil {
			return nil, err
		}
	case ProfileLINK:
		if _, err := NewLINKSelections(identity, i.config.Options.Sources); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported profile %q", i.config.Options.Profile)
	}
	return func(e canbusreg.Evidence) (any, bool) {
		ev, reason, err := i.projectProfileEvaluation(e)
		return ev, err == nil && reason == "" && len(ev) > 0
	}, nil
}

func (i *instance) projectEvaluations(e canbusreg.Evidence) (map[string]roweval.Evaluation, error) {
	ev, _, err := i.projectProfileEvaluation(e)
	return ev, err
}

func (i *instance) projectProfileEvaluation(e canbusreg.Evidence) (map[string]roweval.Evaluation, roweval.Reason, error) {
	switch i.config.Options.Profile {
	case ProfilePCS:
		return i.evaluatePCS(e)
	case ProfileLINK:
		return i.evaluateLINK(e)
	default:
		return nil, "", nil
	}
}

// pcsInput carries the configured selection as the observation context.
// PublisherResolved is the caller's selection of this instance. MeasurementValid
// is the admission the standard rows require before a bound element exists.
// Temperature, count and topology assertions are not options and stay unset.
func pcsInput(sel canbusreg.Selection, e canbusreg.Evidence) canbusreg.PCSStandardPCSInput {
	c := sel.SelectionSpecValue()
	return canbusreg.PCSStandardPCSInput{
		LINKInput:         linkContext(c, e),
		PublisherResolved: true,
		MeasurementValid:  true,
	}
}

func linkContext(c canbusreg.SelectionSpec, e canbusreg.Evidence) canbusreg.LINKInput {
	return canbusreg.LINKInput{
		Evidence: e, Epoch: c.Epoch, Direction: c.Direction, Scope: c.Scope, Bank: c.Bank,
		Endpoint: c.Endpoint, Group: c.Group, Slot: c.Slot,
	}
}

func (i *instance) evaluatePCS(e canbusreg.Evidence) (map[string]roweval.Evaluation, roweval.Reason, error) {
	if i.config.Options.PCS == nil {
		return nil, "", errors.New("options: pcs is required")
	}
	sel, err := NewPCSSelection(e.Interface, *i.config.Options.PCS)
	if err != nil {
		return nil, "", err
	}
	in := pcsInput(sel, e)
	id := e.Frame.ID()
	if id.Extended() {
		return nil, "", nil
	}
	switch id.Value() {
	case 0x311, 0x312, 0x313, 0x314:
		return i.evaluatePCSBasic(sel, in, e)
	case 0x315, 0x316, 0x317, 0x318:
		res, reason, err := pcs.EvaluateGauge(sel, canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: in})
		return res.Rows, reason, err
	case 0x319, 0x322, 0x330:
		res, reason, err := pcs.EvaluateExtrema(sel, in)
		return res.Rows, reason, err
	case 0x320, 0x321, 0x323, 0x324, 0x325:
		res, reason, err := pcs.EvaluateIdentity(sel, canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: in})
		return res.Rows, reason, err
	case 0x326, 0x327, 0x333, 0x334:
		res, reason, err := pcs.EvaluateSlots(sel, canbusreg.PCSSlotPCSInput{PCSIdentityPCSInput: canbusreg.PCSIdentityPCSInput{PCSStandardPCSInput: in}})
		return res.Rows, reason, err
	default:
		return nil, "", nil
	}
}

func (i *instance) evaluatePCSBasic(sel canbusreg.Selection, in canbusreg.PCSStandardPCSInput, e canbusreg.Evidence) (map[string]roweval.Evaluation, roweval.Reason, error) {
	res, reason, err := pcs.Evaluate(sel, in)
	if err != nil || reason != "" {
		return res.Rows, reason, err
	}
	table := pcs.BoundTable()
	extra, err := candidatesOf(table, e, pcs.Qualifiers(sel, in), pcs.Descriptors(), i.projectTemplate(e, policyOf(table.Bindings), ""), projectRoles(i.key, "", table.Bindings), func(at pcs.Selector) bool {
		return at.Identifier == e.Frame.ID().Value() && at.Extended == e.Frame.ID().Extended() && at.DLC == e.Frame.DLC() && at.RawDLC == e.Frame.RawDLC()
	})
	if err != nil {
		return nil, "", err
	}
	return mergeCandidates(res.Rows, extra), "", nil
}

func (i *instance) evaluateLINK(e canbusreg.Evidence) (map[string]roweval.Evaluation, roweval.Reason, error) {
	sels, err := NewLINKSelections(e.Interface, i.config.Options.Sources)
	if err != nil {
		return nil, "", err
	}
	entry, ok := sels.Lookup(uint8(e.Frame.ID().Value()))
	if !ok {
		return nil, "", nil
	}
	in := linkContext(entry.Selection.SelectionSpecValue(), e)
	res, reason, err := linkObservation(entry, in)
	if err != nil || reason != "" {
		return qualifySource(res.Rows, entry.Source), reason, err
	}
	if entry.Coordinator {
		return qualifySource(res.Rows, entry.Source), "", nil
	}
	family := uint16(e.Frame.ID().Value() >> 16)
	table := link.BoundTable()
	q := link.Qualifiers(entry.Selection, in)
	if family == 0x1862 || family == 0x180b {
		table = link.BoundFineTable()
		q = link.FineQualifiers(entry.Selection, in, canbusreg.SummaryGates{})
	}
	source := fmt.Sprintf("%02X", entry.Source)
	extra, err := candidatesOf(table, e, q, link.Descriptors(), i.projectTemplate(e, policyOf(table.Bindings), source), projectRoles(i.key, source, table.Bindings), func(at link.Selector) bool {
		return at.Family == family && at.Extended == e.Frame.ID().Extended() && at.DLC == e.Frame.DLC() && at.RawDLC == e.Frame.RawDLC()
	})
	if err != nil {
		return nil, "", err
	}
	return qualifySource(mergeCandidates(res.Rows, extra), entry.Source), "", nil
}

func linkObservation(entry LINKEntry, in canbusreg.LINKInput) (link.Result, roweval.Reason, error) {
	if entry.Coordinator {
		return link.EvaluateAggregate(entry.Selection, in)
	}
	switch uint16(in.Evidence.Frame.ID().Value() >> 16) {
	case 0x1862, 0x180b:
		return link.EvaluateFine(entry.Selection, in, canbusreg.SummaryGates{})
	case 0x1809, 0x18fb:
		return link.EvaluateIdentity(entry.Selection, in)
	case 0x1850, 0x1851, 0x1852, 0x1853, 0x1861, 0x1869:
		return link.EvaluateDiagnostics(entry.Selection, in)
	default:
		return link.Evaluate(entry.Selection, in)
	}
}

// qualifySource keeps each LINK module's native reading under its own source.
// Fact candidate identifiers are already source-qualified and stay as they are.
func qualifySource(rows map[string]roweval.Evaluation, source uint8) map[string]roweval.Evaluation {
	if len(rows) == 0 {
		return rows
	}
	suffix := fmt.Sprintf(".%02X", source)
	out := make(map[string]roweval.Evaluation, len(rows))
	for id, ev := range rows {
		out[id+suffix] = ev
	}
	return out
}

func (i *instance) projectTemplate(e canbusreg.Evidence, policy semreg.PolicyID, source string) semreg.FactCandidate {
	tpl := i.template(e)
	age := int64(i.config.Options.CacheAge)
	tpl.FreshnessPolicy.PolicyID = policy
	tpl.FreshnessPolicy.FreshForNS = semreg.Uint64(strconv.FormatInt(age, 10))
	tpl.FreshnessPolicy.RetainForNS = semreg.Uint64(strconv.FormatInt(age+1, 10))
	if source != "" {
		tpl.CandidateID = semreg.CandidateID(string(tpl.CandidateID) + "." + source)
	}
	return tpl
}

func policyOf(rows []roweval.BindingRow) semreg.PolicyID {
	for _, row := range rows {
		if row.Read != nil && row.Read.Policy != "" {
			return row.Read.Policy
		}
	}
	return ""
}

func projectRoles(instance semdriver.InstanceKey, source string, bindings []roweval.BindingRow) roweval.Roles {
	suffix := "pack"
	if source != "" {
		suffix += "." + source
	}
	capability := semreg.CapabilityInstanceID("capability." + string(instance) + "." + suffix)
	return roweval.Roles{
		Subjects: map[string]roweval.RoleBinding{
			"pack": {ServiceInstance: semreg.ServiceInstanceID("service." + string(instance) + "." + suffix), CapabilityInstance: &capability},
		},
		Catalog: roleCatalog(bindings),
	}
}

func candidatesOf[S any](table roweval.Table[S], e canbusreg.Evidence, q roweval.Qualifiers, desc roweval.Descriptors[S], template semreg.FactCandidate, roles roweval.Roles, match func(S) bool) (map[string]roweval.Evaluation, error) {
	out := map[string]roweval.Evaluation{}
	for _, row := range table.Native {
		if row.ID == "" || !match(row.At) {
			continue
		}
		one := template
		one.CandidateID = semreg.CandidateID(string(template.CandidateID) + "." + row.ID)
		ev, err := roweval.Evaluate(table, roweval.Acquired[S]{
			Template: one, Roles: roles, Qualifiers: q, Descriptors: desc, Selector: row.At, Payload: e.Frame.Data(),
			Receipt: roweval.Receipt{ReceivedAt: one.Times.ReceivedAt, Monotonic: one.Times.ReceiptMonotonic},
		})
		if err != nil {
			return nil, err
		}
		if ev.Candidate != nil {
			out[row.ID] = ev
		}
	}
	return out, nil
}

func mergeCandidates(rows, extra map[string]roweval.Evaluation) map[string]roweval.Evaluation {
	if len(extra) == 0 {
		return rows
	}
	if rows == nil {
		rows = map[string]roweval.Evaluation{}
	}
	for id, ev := range extra {
		cur := rows[id]
		cur.Candidate, cur.Subject = ev.Candidate, ev.Subject
		if cur.Native == nil {
			cur.Native = ev.Native
		}
		rows[id] = cur
	}
	return rows
}

func (b *nsBook) bindInstanceRows(node semdriver.NodeRef) error {
	switch b.inst.config.Options.Profile {
	case ProfilePCS, ProfileLINK:
		return b.bindProjectRows(node)
	default:
		return b.bindRows(node)
	}
}

func (b *nsBook) bindProjectRows(node semdriver.NodeRef) error {
	id, err := probeIdentity(b.inst.config.Interface)
	if err != nil {
		return err
	}
	rows, err := projectNativeRows(b.inst.config.Options, id)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(rows))
	units := map[string]string{}
	for _, row := range rows {
		if _, ok := units[row.id]; ok {
			continue
		}
		units[row.id] = row.unit
		ids = append(ids, row.id)
	}
	sort.Strings(ids)
	b.mu.Lock()
	for _, local := range ids {
		if _, ok := b.rows[local]; !ok {
			b.rows[local] = nsRow{unit: units[local], validity: "absent"}
		}
	}
	b.mu.Unlock()
	scope := projectScope(b.inst.config.Options)
	for _, local := range ids {
		if err := b.bind(node, readingSpec(local, "canbusreg.native.row", units[local], scope)); err != nil {
			return err
		}
	}
	return nil
}

type namedRow struct {
	id, unit string
}

func projectNativeRows(o Options, id canbus.InterfaceIdentity) ([]namedRow, error) {
	switch o.Profile {
	case ProfilePCS:
		if o.PCS == nil {
			return nil, errors.New("options: pcs is required")
		}
		sel, err := NewPCSSelection(id, *o.PCS)
		if err != nil {
			return nil, err
		}
		return admitRows(pcs.CombinedTable().Native, []roweval.Qualifiers{pcs.Qualifiers(sel, pcsInput(sel, canbusreg.Evidence{}))}), nil
	case ProfileLINK:
		sels, err := NewLINKSelections(id, o.Sources)
		if err != nil {
			return nil, err
		}
		frame := quietFrame()
		var rows []namedRow
		for _, entry := range sels.Entries {
			in := linkContext(entry.Selection.SelectionSpecValue(), canbusreg.Evidence{Frame: frame})
			q := link.FineQualifiers(entry.Selection, in, canbusreg.SummaryGates{})
			suffix := fmt.Sprintf(".%02X", entry.Source)
			for _, row := range admitRows(link.AllTable().Native, []roweval.Qualifiers{q}) {
				row.id += suffix
				rows = append(rows, row)
			}
		}
		return rows, nil
	default:
		return nil, nil
	}
}

func admitRows[S any](rows []roweval.NativeRow[S], qs []roweval.Qualifiers) []namedRow {
	seen := map[string]bool{}
	out := make([]namedRow, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" || seen[row.ID] || !anySelection(row.When, qs) {
			continue
		}
		seen[row.ID] = true
		out = append(out, namedRow{id: row.ID, unit: row.Unit})
	}
	return out
}

func anySelection(when roweval.When, qs []roweval.Qualifiers) bool {
	for _, q := range qs {
		if selectionAdmits(when, q) {
			return true
		}
	}
	return false
}

func selectionAdmits(when roweval.When, q roweval.Qualifiers) bool {
	for key, want := range when.Equal {
		if _, keep := selectionKeys[key]; !keep {
			continue
		}
		if q.Values[key] != want {
			return false
		}
	}
	return true
}

func projectScope(o Options) string {
	switch o.Profile {
	case ProfilePCS:
		if o.PCS != nil && (o.PCS.Scope == "local" || o.PCS.Scope == "aggregate") {
			return o.PCS.Scope
		}
	case ProfileLINK:
		for _, entry := range o.Sources {
			if entry.Direction != coordinatorDirection {
				return "local"
			}
		}
		if len(o.Sources) > 0 {
			return coordinatorScope
		}
	}
	return "instance"
}

func quietFrame() canbus.Frame {
	id, err := canbus.NewExtendedID(0x18000000)
	if err != nil {
		return canbus.Frame{}
	}
	frame, err := canbus.NewFrame(id, make([]byte, 8))
	if err != nil {
		return canbus.Frame{}
	}
	return frame
}
