"""Offline, token-aware provider identifier rename; literals remain unchanged.

Identifiers and the names that comments cite are renamed; string, rune and raw
literals are kept byte for byte. Metadata keys that collide after a rename must
both be lists and are merged. Behavior-name baseline entries whose symbol this
run renames to a name the guard no longer flags are removed.
"""
import json
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from check_behavior_names import TOKENS as CODED  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
RENAMES = {'CXDecodeLINKTelemetry': 'DecodeLINKTelemetry',
 'CXNativeField': 'NativeField',
 'CXNativeResult': 'NativeResult',
 'CXSelection': 'Selection',
 'NewCX15OpaqueStore': 'NewOpaqueStore',
 'NewCX16SelectedProfile': 'NewSelectedProfile',
 'NewCXSelection': 'NewSelection',
 'CX01ConfirmNativeSelection': 'ConfirmNativeSelection',
 'CX01Decode': 'DecodeLINKIdentity',
 'CX01FeatureGrade': 'SelectionFeatureGrade',
 'CX01Grade': 'SelectionGrade',
 'CX01Group': 'SelectionGroup',
 'CX01LINKIdentityAssembler': 'LINKIdentityAssembler',
 'CX01LINKIdentitySnapshot': 'LINKIdentitySnapshot',
 'CX01LINKInput': 'LINKInput',
 'CX01Layout': 'SelectionLayout',
 'CX01NativeIndication': 'SelectionNativeIndication',
 'CX01NewLINKIdentityAssembler': 'NewLINKIdentityAssembler',
 'CX01SelectionSpec': 'SelectionSpec',
 'CX01Snapshot': 'SnapshotLINKIdentity',
 'CX01Spec': 'SelectionSpecValue',
 'CX05DecodePCSStandard': 'DecodePCSStandard',
 'CX05PCSInput': 'PCSStandardPCSInput',
 'CX05PCSResult': 'PCSStandardPCSResult',
 'CX05RetainAccepted': 'RetainAccepted',
 'CX06DecodePCSGauge': 'DecodePCSGauge',
 'CX06DecodePCSIdentity': 'DecodePCSIdentity',
 'CX06PCSInput': 'PCSIdentityPCSInput',
 'CX06SerialAssembler': 'PCSIdentitySerialAssembler',
 'CX06SerialResult': 'PCSIdentitySerialResult',
 'CX07DecodeDTC': 'DecodeDTC',
 'CX07EncodeDTC': 'EncodeDTC',
 'CX07ExplainLocalSelection': 'ExplainLocalSelection',
 'CX07LocalSelection': 'DTCLocalSelection',
 'CX08CounterInput': 'CounterInput',
 'CX08DecodePCSSlots': 'DecodePCSSlots',
 'CX08ExplainCounter': 'ExplainCounter',
 'CX08ExplainTail': 'ExplainTail',
 'CX08PCSInput': 'PCSSlotPCSInput',
 'CX08RefineCounter': 'RefineCounter',
 'CX09Decode': 'DecodePCSExtendedFrame',
 'CX09DecodePCSExtended': 'DecodePCSExtended',
 'CX09NewPCSExtendedDecoder': 'NewPCSExtendedDecoder',
 'CX09PCSExtendedDecoder': 'PCSExtendedDecoder',
 'CX09Snapshot': 'SnapshotPCSExtended',
 'CX10Decode': 'DecodeParallelIdentity',
 'CX10DecodeParallel': 'ParallelIdentityDecodeParallel',
 'CX10NewParallelIdentityAssembler': 'NewParallelIdentityAssembler',
 'CX10ParallelIdentityAssembler': 'ParallelIdentityAssembler',
 'CX10ParallelIdentitySnapshot': 'ParallelIdentitySnapshot',
 'CX10Snapshot': 'SnapshotParallelIdentity',
 'CX11DecodeParallel': 'ParallelReleaseDecodeParallel',
 'CX11ParallelContext': 'ParallelContext',
 'CX12Decode': 'DecodeLINKVector',
 'CX12DecodeLINKBalance': 'DecodeLINKBalance',
 'CX12DecodeLINKCellMean': 'DecodeLINKCellMean',
 'CX12DecodeLINKTemperatureSummary': 'DecodeLINKTemperatureSummary',
 'CX12LINKVector': 'LINKVector',
 'CX12MeasurementGate': 'MeasurementGate',
 'CX12NewLINKCells': 'NewLINKCells',
 'CX12NewLINKFixed16Cells': 'NewLINKFixed16Cells',
 'CX12NewLINKTemperatures': 'NewLINKTemperatures',
 'CX12Snapshot': 'SnapshotLINKVector',
 'CX12SummaryGates': 'SummaryGates',
 'CX12VectorSnapshot': 'VectorSnapshot',
 'CX13DecodeLINKDiagnostics': 'DecodeLINKDiagnostics',
 'CX13LabelCondition': 'LabelCondition',
 'CX14DecodePCSSummaries': 'DecodePCSSummaries',
 'CX14PCSInput': 'PCSSummaryPCSInput',
 'CX14PCSResult': 'PCSSummaryPCSResult',
 'CX14SummaryBit': 'SummaryBit',
 'CX15CapabilityKey': 'CapabilityKey',
 'CX15CapabilityState': 'CapabilityState',
 'CX15ClassifyReceiver': 'ClassifyReceiver',
 'CX15Observed': 'ReceiverCapabilityObserved',
 'CX15OpaqueStore': 'OpaqueStore',
 'CX15Qualified': 'ReceiverCapabilityQualified',
 'CX15ReceiverContext': 'ReceiverContext',
 'CX15ReceiverResult': 'ReceiverResult',
 'CX15Unknown': 'ReceiverCapabilityUnknown',
 'CX15Unsupported': 'ReceiverCapabilityUnsupported',
 'CX16Diagnostic': 'Diagnostic',
 'CX16Projection': 'ReplayProjection',
 'CX16ReplayInput': 'ReplayInput',
 'CX17NewLINKProvider': 'NewLINKProvider',
 'CX17NewPCSProvider': 'NewPCSProvider',
 'CX17Observation': 'Observation',
 'CX17Provider': 'Provider',
 'CX17Raw': 'ProjectProviderRaw',
 'CX17Receive': 'ProjectProviderReceive',
 'CX17ReceiveSource': 'ReceiveSource',
 'CX17Snapshot': 'ProjectProviderSnapshot',
 'O08Evidence': 'Evidence',
 'O08Generation': 'Generation',
 'O08GrowattProvider': 'GrowattBMSProvider',
 'O08GrowattSnapshot': 'GrowattBMSSnapshot',
 'O08HasRaw': 'HasRaw',
 'O08NewGrowattProvider': 'NewGrowattBMSProvider',
 'O08Observation': 'BMSObservation',
 'O08Projection': 'Projection',
 'O08ProjectionGeneration': 'ProjectionGeneration',
 'O08Raw': 'Raw',
 'O08RawGeneration': 'RawGeneration',
 'O08Receive': 'Receive',
 'O08ReceiveSource': 'BMSReceiveSource',
 'O08RevisionV104': 'BMSRevisionV104',
 'O08Snapshot': 'Snapshot',
 'O08Start': 'Start',
 'O08State': 'State',
 'O08Stop': 'Stop',
 'cx01ASCII': 'selectionASCII',
 'cx01Input': 'selectionTestInput',
 'cx01Invalidate': 'selectionInvalidate',
 'cx01LINKAdmission': 'lINKAdmission',
 'cx01LINKCoordinatorAdmission': 'lINKCoordinatorAdmission',
 'cx01LINKDateAdmission': 'lINKDateAdmission',
 'cx01LINKFrameAdmission': 'lINKFrameAdmission',
 'cx01LINKReportAdmission': 'lINKReportAdmission',
 'cx01Result': 'selectionResult',
 'cx01Select': 'selectionTestSelect',
 'cx01ShortFrame': 'selectionTestShortFrame',
 'cx01Snapshot': 'snapshotLINKIdentity',
 'cx01Spec': 'selectionTestSpec',
 'cx05Fixture': 'standardTestFixture',
 'cx05Reselect': 'standardTestReselect',
 'cx06Add': 'pCSIdentityAdd',
 'cx06Admit': 'pCSIdentityAdmit',
 'cx06Fixture': 'identityTestFixture',
 'cx06Fragment': 'identityTestFragment',
 'cx06Want': 'identityTestWant',
 'cx07Categories': 'categories',
 'cx07Dictionary': 'dictionary',
 'cx07Expected': 'dtcTestExpected',
 'cx07Member': 'dtcTestMember',
 'cx08Fixture': 'slotTestFixture',
 'cx08Release11': 'release11',
 'cx08Want': 'slotTestWant',
 'cx09Input': 'extendedTestInput',
 'cx09Select': 'extendedTestSelect',
 'cx09Spec': 'extendedTestSpec',
 'cx09Withheld': 'extendedTestWithheld',
 'cx10Admission': 'parallelIdentityAdmission',
 'cx10Family': 'parallelIdentityFamily',
 'cx10Fragment': 'parallelIdentityTestFragment',
 'cx10Input': 'parallelIdentityTestInput',
 'cx10Selection': 'parallelIdentityTestSelection',
 'cx10Spec': 'parallelIdentityTestSpec',
 'cx11Decode': 'parallelReleaseTestDecode',
 'cx11DiagnosticNegative': 'parallelReleaseTestDiagnosticNegative',
 'cx11Source': 'parallelReleaseTestSource',
 'cx11SourceValue': 'sourceValue',
 'cx11Spec': 'parallelReleaseTestSpec',
 'cx11Want': 'parallelReleaseTestWant',
 'cx12Admit': 'lINKVectorAdmit',
 'cx12Age': 'lINKVectorAge',
 'cx12Contributors': 'lINKVectorContributors',
 'cx12Field': 'lINKVectorField',
 'cx12Gate': 'lINKVectorGate',
 'cx12GateRejection': 'lINKVectorGateRejection',
 'cx12Gates': 'vectorTestGates',
 'cx12NewVector': 'lINKVectorNewVector',
 'cx12OtherInterface': 'vectorTestOtherInterface',
 'cx12Sensors': 'vectorTestSensors',
 'cx12SummaryInput': 'vectorTestSummaryInput',
 'cx12Words': 'vectorTestWords',
 'cx13Index': 'diagnosticTestIndex',
 'cx13ModernOnly': 'diagnosticTestModernOnly',
 'cx13Names': 'diagnosticTestNames',
 'cx13Standard': 'diagnosticTestStandard',
 'cx14Expected': 'summaryTestExpected',
 'cx14Fixture': 'summaryTestFixture',
 'cx14Payload': 'summaryTestPayload',
 'cx15Capability': 'receiverCapabilityCapability',
 'cx15Describe': 'receiverCapabilityDescribe',
 'cx15Fixture': 'capabilityTestFixture',
 'cx15Input': 'capabilityTestInput',
 'cx15MutateField': 'capabilityTestMutateField',
 'cx15PIDName': 'receiverCapabilityPIDName',
 'cx15ReceiverRules': 'receiverRules',
 'cx15Retained': 'receiverCapabilityRetained',
 'cx15Rule': 'receiverCapabilityRule',
 'cx15Rules': 'receiverCapabilityRules',
 'cx15SameStream': 'receiverCapabilitySameStream',
 'cx16Classify': 'replayTestClassify',
 'cx16Context': 'replayContext',
 'cx16Decode': 'fixture16Decode',
 'cx16Entry': 'replayEntry',
 'cx16Fixture': 'replayTestFixture',
 'cx16Frame': 'replayTestFrame',
 'cx16LINK': 'replayTestLINK',
 'cx16OverlappingGeometry': 'replayOverlappingGeometry',
 'cx16Profile': 'replayTestProfile',
 'cx16Reselect': 'replayTestReselect',
 'cx16RetentionKey': 'replayRetentionKey',
 'cx16SelectedProfile': 'replaySelectedProfile',
 'cx16Withheld': 'replayTestWithheld',
 'cx17Apply': 'projectProviderApply',
 'cx17Association': 'projectProviderAssociation',
 'cx17Consume': 'projectProviderConsume',
 'cx17Demote': 'projectProviderDemote',
 'cx17ErrorSource': 'providerTestErrorSource',
 'cx17Event': 'providerTestEvent',
 'cx17ExtendedKind': 'projectProviderExtendedKind',
 'cx17Fake': 'providerTestFake',
 'cx17GroupFixture': 'providerTestGroupFixture',
 'cx17IdentityKey': 'projectProviderIdentityKey',
 'cx17Interface': 'providerTestInterface',
 'cx17LINKSerial': 'projectProviderLINKSerial',
 'cx17LateSource': 'providerTestLateSource',
 'cx17New': 'projectProviderNew',
 'cx17Raw': 'projectProviderRaw',
 'cx17Receive': 'projectProviderReceive',
 'cx17Reconcile': 'projectProviderReconcile',
 'cx17Send': 'providerTestSend',
 'cx17SerialConflict': 'projectProviderSerialConflict',
 'cx17Source': 'providerTestSource',
 'cx17Stop': 'projectProviderStop',
 'cx17Withheld': 'projectProviderWithheld',
 'cx17Withhold': 'projectProviderWithhold',
 'o08Apply': 'bmsApply',
 'o08Bad': 'bmsBad',
 'o08Before': 'bmsBefore',
 'o08Bytes': 'bmsBytes',
 'o08Cancel': 'bmsCancel',
 'o08Candidate': 'bmsCandidate',
 'o08Case': 'bmsCase',
 'o08Ctx': 'bmsCtx',
 'o08Done': 'bmsDone',
 'o08E': 'bmsE',
 'o08Entered': 'bmsEntered',
 'o08Err': 'bmsErr',
 'o08Evidence': 'bmsEvidence',
 'o08Feed': 'bmsFeed',
 'o08Frame': 'bmsFrame',
 'o08Generation': 'bmsGeneration',
 'o08Got': 'bmsGot',
 'o08HasSequence': 'bmsHasSequence',
 'o08I': 'bmsI',
 'o08ID': 'bmsID',
 'o08Identity': 'bmsIdentity',
 'o08Index': 'bmsIndex',
 'o08Interface': 'bmsInterface',
 'o08Invalid': 'bmsInvalid',
 'o08Late': 'bmsLate',
 'o08Lifecycle': 'bmsLifecycle',
 'o08Mu': 'bmsMu',
 'o08Name': 'bmsName',
 'o08NativeFrame': 'bmsNativeFrame',
 'o08NativeID': 'bmsNativeID',
 'o08NewSource': 'bmsNewSource',
 'o08OK': 'bmsOK',
 'o08Observation': 'bmsObservation',
 'o08P': 'bmsP',
 'o08PreviousDone': 'bmsPreviousDone',
 'o08Projection': 'bmsProjection',
 'o08Provider': 'bmsProvider',
 'o08R': 'bmsR',
 'o08Receive': 'bmsReceive',
 'o08Replay': 'bmsReplay',
 'o08Result': 'bmsResult',
 'o08Results': 'bmsResults',
 'o08Revision': 'bmsRevision',
 'o08Running': 'bmsRunning',
 'o08S': 'bmsS',
 'o08Seq': 'bmsSeq',
 'o08Sequence': 'bmsSequence',
 'o08Snapshot': 'bmsSnapshot',
 'o08Source': 'bmsSource',
 'o08Start': 'bmsStart',
 'o08Stop': 'bmsStop',
 'o08Successor': 'bmsSuccessor',
 'o08T': 'bmsT',
 'o08Tuple': 'bmsTuple'}
FILES = ['growatt_link_balance.go',
 'growatt_link_balance_test.go',
 'growatt_link_cells.go',
 'growatt_link_cells_test.go',
 'growatt_link_diagnostics.go',
 'growatt_link_diagnostics_test.go',
 'growatt_link_identity.go',
 'growatt_link_identity_test.go',
 'growatt_link_telemetry.go',
 'growatt_link_telemetry_test.go',
 'growatt_link_temperatures.go',
 'growatt_link_temperatures_test.go',
 'growatt_pcs_extended.go',
 'growatt_pcs_extended_test.go',
 'growatt_pcs_gauge.go',
 'growatt_pcs_gauge_test.go',
 'growatt_pcs_identity.go',
 'growatt_pcs_identity_test.go',
 'growatt_pcs_parallel_base.go',
 'growatt_pcs_parallel_base_test.go',
 'growatt_pcs_parallel_identity.go',
 'growatt_pcs_parallel_identity_test.go',
 'growatt_pcs_parallel_release11.go',
 'growatt_pcs_parallel_release11_test.go',
 'growatt_pcs_slots.go',
 'growatt_pcs_slots_test.go',
 'growatt_pcs_standard.go',
 'growatt_pcs_standard_test.go',
 'growatt_pcs_summaries.go',
 'growatt_pcs_summaries_test.go',
 'growatt_project_admission.go',
 'growatt_project_admission_test.go',
 'growatt_project_capabilities.go',
 'growatt_project_capabilities_test.go',
 'growatt_project_conditions.go',
 'growatt_project_conditions_test.go',
 'growatt_project_conformance_test.go',
 'growatt_project_counter.go',
 'growatt_project_counter_test.go',
 'growatt_project_dtc.go',
 'growatt_project_dtc_test.go',
 'growatt_project_opaque.go',
 'growatt_project_opaque_test.go',
 'growatt_project_provider.go',
 'growatt_project_provider_test.go',
 'growatt_project_selection.go',
 'growatt_project_selection_test.go',
 'naming_contract_test.go',
 'o08_growatt_provider.go',
 'o08_growatt_provider_test.go',
 'growatt_bms_provider.go',
 'growatt_bms_provider_test.go']
METADATA = ['naming-consumers.json',
 'naming-inventory.json',
 'scripts/behavior-name-baseline.json',
 'scripts/go-lint-baseline.json',
 'scripts/size-baseline.json']
MOVES = {"o08_growatt_provider.go": "growatt_bms_provider.go", "o08_growatt_provider_test.go": "growatt_bms_provider_test.go"}
TOKEN = re.compile(r"//[^\n]*|/\*[\s\S]*?\*/|\"(?:\\.|[^\"\\])*\"|`[^`]*`|'(?:\\.|[^'\\])*'|\b[A-Za-z_]\w*\b")
WORD = re.compile(r"\b[A-Za-z_]\w*\b")


def rename_words(text):
    return WORD.sub(lambda match: RENAMES.get(match[0], match[0]), text)


def rewrite(source):
    def token(match):
        if match[0].startswith(("//", "/*")):
            return rename_words(match[0])
        return RENAMES.get(match[0], match[0])
    return TOKEN.sub(token, source)


def merge_keys(items):
    merged = {}
    for key, value in items:
        if key not in merged:
            merged[key] = value
        elif isinstance(merged[key], list) and isinstance(value, list):
            merged[key] = sorted(set(merged[key]) | set(value))
        else:
            raise SystemExit("renamed metadata key collides: " + key)
    return merged


def metadata(value):
    if isinstance(value, dict):
        return merge_keys((metadata(k), metadata(v)) for k, v in value.items())
    if isinstance(value, list):
        return [metadata(v) for v in value]
    if isinstance(value, str):
        value = MOVES.get(value, value)
        return rename_words(value)
    return value


def prune_baseline(original, updated):
    return [new for old, new in zip(original, updated)
            if new["symbol"] == old["symbol"] or CODED.search(new["symbol"])]


def main():
    for name in FILES:
        path = ROOT / name
        if path.exists():
            source = rewrite(path.read_text())
            formatted = subprocess.run(["gofmt"], input=source, text=True,
                                       capture_output=True, check=True).stdout
            path.write_text(formatted)
    for old, new in MOVES.items():
        path = ROOT / old
        if path.exists():
            if (ROOT / new).exists():
                raise SystemExit("destination already exists: " + new)
            path.rename(ROOT / new)
    for name in METADATA:
        path = ROOT / name
        original = json.loads(path.read_text())
        updated = metadata(original)
        if name == "scripts/behavior-name-baseline.json":
            updated["baseline"] = prune_baseline(original["baseline"], updated["baseline"])
        if original != updated:
            path.write_text(json.dumps(updated, indent=2) + "\n")


if __name__ == "__main__":
    main()
