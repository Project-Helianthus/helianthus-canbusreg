// Package certification holds, checks and looks up the values that
// certificates state about the measurement accuracy of the devices a driver
// serves. The record form follows the certification references contract:
// one data file per device family, apart from everything a device publishes.
package certification

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ContractID is the contract identifier every document carries.
const ContractID = "helianthus.semreg.certification-references/v1"

// SourceCertification is the one source category.
const SourceCertification = "certification"

// Entry categories.
const (
	CategoryReference   = "reference"
	CategoryGap         = "gap"
	CategoryUnavailable = "unavailable"
)

// Firmware forms.
const (
	FormNotStated = "not_stated"
	FormRelease   = "release"
	FormRange     = "range"
)

// Coverage results of a release against a firmware record.
const (
	Covered    = "covered"
	NotCovered = "not_covered"
	NotStated  = "not_stated"
)

// SchemaFile is the name of the shared schema among the embedded files.
const SchemaFile = "references.schema.json"

// Stable failure reasons.
const (
	ReasonSchema                 = "schema"
	ReasonSourceNotCertification = "source_not_certification"
	ReasonUnknownMember          = "unknown_member"
	ReasonMissingMember          = "missing_member"
	ReasonStatedValueNotAllowed  = "stated_value_not_allowed"
	ReasonRefusalMissing         = "refusal_missing"
	ReasonInvalidValue           = "invalid_value"
	ReasonFirmwareInvalid        = "firmware_invalid"
	ReasonFirmwareUpperUnstated  = "firmware_upper_bound_unstated"
	ReasonStatedValueInvalid     = "stated_value_invalid"
	ReasonCategoryConflict       = "category_conflict"
)

//go:embed *.json
var files embed.FS

// Decimal is a canonical decimal: Coefficient * 10^Exponent10.
type Decimal struct {
	Coefficient string
	Exponent10  int
}

// Rat returns the exact value of the decimal.
func (d Decimal) Rat() *big.Rat {
	c, _ := new(big.Int).SetString(d.Coefficient, 10)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(d.Exponent10))), nil)
	if d.Exponent10 < 0 {
		return new(big.Rat).SetFrac(c, scale)
	}
	return new(big.Rat).SetInt(new(big.Int).Mul(c, scale))
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Firmware states the releases a record covers.
type Firmware struct {
	Form               string
	Release            string
	From               string
	To                 string
	UpperBoundStatedBy string
}

// StatedValue carries only the members the certificate states.
type StatedValue struct {
	MeasurementType string
	Measured        *bool
	RatioMaximum    *Decimal
	MeasuredMinimum *Decimal
	MeasuredMaximum *Decimal
	RangeMinimum    *Decimal
	RangeMaximum    *Decimal
	Statement       string
}

// Entry is one record of a document. CertificateNumber, IssueDate and
// StatedValue are set for a reference only; Refusal for an unavailable entry only.
type Entry struct {
	Source            string
	Category          string
	Driver            string
	Vendor            string
	Flavor            string
	Model             string
	Firmware          Firmware
	Issuer            string
	CertificateNumber string
	IssueDate         string
	StatedValue       *StatedValue
	URL               string
	RetrievedOn       string
	Refusal           string
}

// Document is a loaded certification references file.
type Document struct {
	Entries []Entry
}

// Finding is the first failure of a document. Entry is -1 when no entry is named.
type Finding struct {
	Reason string
	Entry  int
	Member string
}

// Error describes the finding.
func (f *Finding) Error() string {
	text := "certification references rejected: " + f.Reason
	if f.Entry >= 0 {
		text += fmt.Sprintf(" entry=%d", f.Entry)
	}
	if f.Member != "" {
		text += " member=" + f.Member
	}
	return text
}

func reject(reason string, entry int, member string) error {
	return &Finding{Reason: reason, Entry: entry, Member: member}
}

var (
	commonMembers = []string{"source", "category", "driver", "vendor", "flavor", "model", "firmware"}
	locationOrder = []string{"issuer", "url", "retrieved_on"}
	categoryExtra = map[string][]string{
		CategoryReference:   {"issuer", "certificate_number", "issue_date", "stated_value", "url", "retrieved_on"},
		CategoryGap:         locationOrder,
		CategoryUnavailable: append(append([]string{}, locationOrder...), "refusal"),
	}
	textMembers    = []string{"driver", "vendor", "flavor", "model", "issuer", "certificate_number"}
	dateMembers    = []string{"issue_date", "retrieved_on"}
	decimalMembers = []string{"ratio_maximum", "measured_minimum", "measured_maximum", "range_minimum", "range_maximum"}
	releasePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	datePattern    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	urlPattern     = regexp.MustCompile(`^https://[^ ]+$`)
	coefficientRe  = regexp.MustCompile(`^(0|-?[1-9]([0-9]*[1-9])?)$`)
	integerRe      = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	tokenPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Load parses and checks a document. A repeated member name in any object is
// rejected before the entry checks run.
func Load(data []byte) (*Document, error) {
	if err := rejectDuplicates(data); err != nil {
		return nil, reject(ReasonSchema, -1, "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, reject(ReasonSchema, -1, "")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, reject(ReasonSchema, -1, "")
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, reject(ReasonSchema, -1, "")
	}
	rawEntries, ok := top["entries"].([]any)
	if !ok {
		return nil, reject(ReasonSchema, -1, "")
	}
	for index, raw := range rawEntries {
		if err := checkEntry(raw, index); err != nil {
			return nil, err
		}
	}
	if len(top) != 2 || top["contract"] != ContractID || len(rawEntries) == 0 {
		return nil, reject(ReasonSchema, -1, "")
	}
	document := &Document{}
	for _, raw := range rawEntries {
		document.Entries = append(document.Entries, buildEntry(raw.(map[string]any)))
	}
	if err := checkCategories(document.Entries); err != nil {
		return nil, err
	}
	return document, nil
}

func rejectDuplicates(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return walkDuplicates(decoder)
}

func walkDuplicates(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '[' {
		for decoder.More() {
			if err := walkDuplicates(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, _ := keyToken.(string)
		if seen[key] {
			return errors.New("duplicate member " + key)
		}
		seen[key] = true
		if err := walkDuplicates(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func checkEntry(raw any, index int) error {
	entry, ok := raw.(map[string]any)
	if !ok {
		return reject(ReasonSchema, index, "")
	}
	for _, check := range []func(map[string]any) *Finding{
		checkSource, checkCategory, checkMembers, checkValues, checkFirmware, checkStatedValue,
	} {
		if found := check(entry); found != nil {
			found.Entry = index
			return found
		}
	}
	return nil
}

func found(reason, member string) *Finding {
	return &Finding{Reason: reason, Member: member}
}

func checkSource(entry map[string]any) *Finding {
	if value, present := entry["source"]; present && value != SourceCertification {
		return found(ReasonSourceNotCertification, "source")
	}
	return nil
}

func checkCategory(entry map[string]any) *Finding {
	value, present := entry["category"]
	if !present {
		return found(ReasonMissingMember, "category")
	}
	if name, ok := value.(string); !ok || categoryExtra[name] == nil {
		return found(ReasonInvalidValue, "category")
	}
	return nil
}

func allowedMembers(category string) []string {
	return append(append([]string{}, commonMembers...), categoryExtra[category]...)
}

func isText(value any) bool {
	text, ok := value.(string)
	return ok && text != ""
}

func checkMembers(entry map[string]any) *Finding {
	category := entry["category"].(string)
	allowed := allowedMembers(category)
	if _, present := entry["stated_value"]; present && category != CategoryReference {
		return found(ReasonStatedValueNotAllowed, "stated_value")
	}
	var extra []string
	for name := range entry {
		if !contains(allowed, name) {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return found(ReasonUnknownMember, extra[0])
	}
	if category == CategoryUnavailable && !isText(entry["refusal"]) {
		return found(ReasonRefusalMissing, "refusal")
	}
	for _, name := range allowed {
		if _, present := entry[name]; !present {
			return found(ReasonMissingMember, name)
		}
	}
	return nil
}

func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

func isDate(value any) bool {
	text, ok := value.(string)
	if !ok || !datePattern.MatchString(text) {
		return false
	}
	if strings.HasPrefix(text, "0000-") {
		return false
	}
	_, err := time.Parse("2006-01-02", text)
	return err == nil
}

func checkValues(entry map[string]any) *Finding {
	for _, name := range textMembers {
		if value, present := entry[name]; present && !isText(value) {
			return found(ReasonInvalidValue, name)
		}
	}
	for _, name := range dateMembers {
		if value, present := entry[name]; present && !isDate(value) {
			return found(ReasonInvalidValue, name)
		}
	}
	if text, ok := entry["url"].(string); !ok || !urlPattern.MatchString(text) {
		return found(ReasonInvalidValue, "url")
	}
	return nil
}

func isRelease(value any) bool {
	text, ok := value.(string)
	return ok && releasePattern.MatchString(text)
}

func checkFirmware(entry map[string]any) *Finding {
	firmware, ok := entry["firmware"].(map[string]any)
	form, _ := firmware["form"].(string)
	var members []string
	switch form {
	case FormNotStated:
	case FormRelease:
		members = []string{"release"}
	case FormRange:
		members = []string{"from", "to", "upper_bound_stated_by"}
	default:
		return found(ReasonFirmwareInvalid, "firmware")
	}
	if !ok {
		return found(ReasonFirmwareInvalid, "firmware")
	}
	if form == FormRange {
		by, _ := firmware["upper_bound_stated_by"].(string)
		if by != "certificate" && by != "vendor_document" {
			return found(ReasonFirmwareUpperUnstated, "firmware")
		}
	}
	if len(firmware) != len(members)+1 {
		return found(ReasonFirmwareInvalid, "firmware")
	}
	for _, name := range members {
		if _, present := firmware[name]; !present {
			return found(ReasonFirmwareInvalid, "firmware")
		}
	}
	for _, name := range []string{"release", "from", "to"} {
		if value, present := firmware[name]; present && !isRelease(value) {
			return found(ReasonFirmwareInvalid, "firmware")
		}
	}
	if form == FormRange && compareReleases(firmware["from"].(string), firmware["to"].(string)) > 0 {
		return found(ReasonFirmwareInvalid, "firmware")
	}
	return nil
}

func isDecimal(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 2 {
		return false
	}
	coefficient, ok := object["coefficient"].(string)
	if !ok || !coefficientRe.MatchString(coefficient) {
		return false
	}
	number, ok := object["exponent10"].(json.Number)
	if !ok || !integerRe.MatchString(number.String()) {
		return false
	}
	exponent, err := number.Int64()
	if err != nil || exponent < -18 || exponent > 18 {
		return false
	}
	return coefficient != "0" || exponent == 0
}

func checkStatedValue(entry map[string]any) *Finding {
	raw, present := entry["stated_value"]
	if !present {
		return nil
	}
	problem := found(ReasonStatedValueInvalid, "stated_value")
	stated, ok := raw.(map[string]any)
	if !ok {
		return problem
	}
	kind, _ := stated["measurement_type"].(string)
	if !tokenPattern.MatchString(kind) {
		return problem
	}
	values := 0
	for name, value := range stated {
		switch {
		case name == "measurement_type":
		case name == "measured":
			if _, isBool := value.(bool); !isBool {
				return problem
			}
			values++
		case name == "statement":
			if !isText(value) {
				return problem
			}
			values++
		case contains(decimalMembers, name):
			if !isDecimal(value) {
				return problem
			}
			values++
		default:
			return problem
		}
	}
	if values == 0 {
		return problem
	}
	return nil
}

func identity(entry Entry) string {
	encoded, _ := json.Marshal([]any{entry.Driver, entry.Vendor, entry.Flavor, entry.Model, entry.Firmware})
	return string(encoded)
}

func checkCategories(entries []Entry) error {
	seen := map[string]string{}
	for index, entry := range entries {
		key := identity(entry)
		if category, ok := seen[key]; ok && category != entry.Category {
			return reject(ReasonCategoryConflict, index, "")
		}
		seen[key] = entry.Category
	}
	return nil
}

func text(entry map[string]any, name string) string {
	value, _ := entry[name].(string)
	return value
}

func decimal(raw any) *Decimal {
	object := raw.(map[string]any)
	exponent, _ := object["exponent10"].(json.Number).Int64()
	return &Decimal{Coefficient: object["coefficient"].(string), Exponent10: int(exponent)}
}

func buildEntry(raw map[string]any) Entry {
	firmware := raw["firmware"].(map[string]any)
	entry := Entry{
		Source: text(raw, "source"), Category: text(raw, "category"),
		Driver: text(raw, "driver"), Vendor: text(raw, "vendor"),
		Flavor: text(raw, "flavor"), Model: text(raw, "model"),
		Firmware: Firmware{
			Form: text(firmware, "form"), Release: text(firmware, "release"),
			From: text(firmware, "from"), To: text(firmware, "to"),
			UpperBoundStatedBy: text(firmware, "upper_bound_stated_by"),
		},
		Issuer: text(raw, "issuer"), CertificateNumber: text(raw, "certificate_number"),
		IssueDate: text(raw, "issue_date"), URL: text(raw, "url"),
		RetrievedOn: text(raw, "retrieved_on"), Refusal: text(raw, "refusal"),
	}
	if stated, ok := raw["stated_value"].(map[string]any); ok {
		value := &StatedValue{MeasurementType: text(stated, "measurement_type"), Statement: text(stated, "statement")}
		if measured, ok := stated["measured"].(bool); ok {
			value.Measured = &measured
		}
		for name, slot := range map[string]**Decimal{
			"ratio_maximum": &value.RatioMaximum, "measured_minimum": &value.MeasuredMinimum,
			"measured_maximum": &value.MeasuredMaximum, "range_minimum": &value.RangeMinimum,
			"range_maximum": &value.RangeMaximum,
		} {
			if member, ok := stated[name]; ok {
				*slot = decimal(member)
			}
		}
		entry.StatedValue = value
	}
	return entry
}

func releaseKey(release string) []*big.Int {
	var key []*big.Int
	for _, part := range strings.Split(release, ".") {
		number, _ := new(big.Int).SetString(part, 10)
		key = append(key, number)
	}
	return key
}

func compareReleases(left, right string) int {
	a, b := releaseKey(left), releaseKey(right)
	for index := 0; index < len(a) || index < len(b); index++ {
		x, y := new(big.Int), new(big.Int)
		if index < len(a) {
			x = a[index]
		}
		if index < len(b) {
			y = b[index]
		}
		if c := x.Cmp(y); c != 0 {
			return c
		}
	}
	return 0
}

// Coverage returns Covered, NotCovered or NotStated for a release against a
// firmware record. Firmware is a floor: a stated release covers itself and
// every later release, and no earlier one.
func Coverage(firmware Firmware, release string) string {
	if firmware.Form == FormNotStated {
		return NotStated
	}
	floor := firmware.Release
	if firmware.Form == FormRange {
		floor = firmware.From
	}
	if compareReleases(release, floor) < 0 {
		return NotCovered
	}
	if firmware.Form == FormRange && compareReleases(release, firmware.To) > 0 {
		return NotCovered
	}
	return Covered
}

// Query names one device. Release is the firmware release of the device; an
// empty Release is a device whose release is unknown.
type Query struct {
	Driver  string
	Vendor  string
	Flavor  string
	Model   string
	Release string
}

// Lookup returns, unchanged and in file order, the entries of the document
// whose driver, vendor, flavor and model equal the query and whose firmware
// record covers the release or records the firmware as not stated. A gap or
// an unavailable entry is returned as such. A release that is not
// dot-separated integers, or an unknown release, matches only a record with
// firmware not stated.
func (d *Document) Lookup(query Query) []Entry {
	var matches []Entry
	for _, entry := range d.Entries {
		if entry.Driver != query.Driver || entry.Vendor != query.Vendor ||
			entry.Flavor != query.Flavor || entry.Model != query.Model {
			continue
		}
		if entry.Firmware.Form == FormNotStated {
			matches = append(matches, entry)
			continue
		}
		if releasePattern.MatchString(query.Release) && Coverage(entry.Firmware, query.Release) == Covered {
			matches = append(matches, entry)
		}
	}
	return matches
}

// Registry is the union of the data files embedded in the package.
type Registry struct {
	Documents map[string]*Document
}

// LoadEmbedded loads every embedded data file; the schema file is not data.
func LoadEmbedded() (*Registry, error) {
	names, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	registry := &Registry{Documents: map[string]*Document{}}
	for _, name := range names {
		if name.Name() == SchemaFile {
			continue
		}
		data, err := files.ReadFile(name.Name())
		if err != nil {
			return nil, err
		}
		document, err := Load(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name.Name(), err)
		}
		registry.Documents[name.Name()] = document
	}
	return registry, nil
}

// Lookup returns the matches of every document, in file-name order.
func (r *Registry) Lookup(query Query) []Entry {
	names := make([]string, 0, len(r.Documents))
	for name := range r.Documents {
		names = append(names, name)
	}
	sort.Strings(names)
	var matches []Entry
	for _, name := range names {
		matches = append(matches, r.Documents[name].Lookup(query)...)
	}
	return matches
}
