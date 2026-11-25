package gree

import (
	"reflect"
	"testing"

	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1"
	"github.com/Project-Helianthus/helianthus-semreg/semreg/v1/driver"
)

func TestCircuitMembershipIsRefusedWithConfigurationOrGateRuleAlone(t *testing.T) {
	checkRelationBasis(t, "refrigerant_circuit_member", semreg.RelationConfiguration, false)
	checkRelationBasis(t, "refrigerant_circuit_member", semreg.RelationGateRule, false)
}

func TestCircuitMembershipIsAcceptedWithNativeObservation(t *testing.T) {
	checkRelationBasis(t, "refrigerant_circuit_member", semreg.RelationNativeObservation, true)
}

func TestOtherRelationsKeepTheirBasisRules(t *testing.T) {
	for _, relation := range []struct {
		name          string
		configuration bool
		gateRule      bool
	}{
		{"air_supply", true, false},
		{"system_member", true, true},
		{"generator_compressor", false, true},
	} {
		checkRelationBasis(t, relation.name, semreg.RelationConfiguration, relation.configuration)
		checkRelationBasis(t, relation.name, semreg.RelationGateRule, relation.gateRule)
		checkRelationBasis(t, relation.name, semreg.RelationNativeObservation, false)
	}
}

func checkRelationBasis(t *testing.T, name string, class semreg.RelationBasis, want bool) {
	t.Helper()
	t.Run(name+"/"+string(class), func(t *testing.T) {
		from := driver.NodeRef{Bridge: "bridge.synthetic", ID: "resource.synthetic.parent"}
		to := driver.NodeRef{Bridge: "bridge.synthetic", ID: "resource.synthetic.member"}
		for _, trial := range []struct {
			name  string
			basis []semreg.EvidenceRef
			want  bool
		}{
			{"with_evidence", evidence([]byte{7}), want},
			{"without_evidence", nil, false},
		} {
			t.Run(trial.name, func(t *testing.T) {
				claim, ok := Relation(name, from, to, class, trial.basis)
				if ok != trial.want || (!ok && !reflect.DeepEqual(claim, driver.LinkClaim{})) {
					t.Fatalf("Relation returned (%+v, %t); want admitted=%t and no claim on refusal", claim, ok, trial.want)
				}
			})
		}
	})
}
