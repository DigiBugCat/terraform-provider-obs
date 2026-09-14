package tfprovider

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
	"testing"
)

func TestGroupParentIndexCanAlreadyConvergeWithoutHidingOtherChanges(t *testing.T) {
	before := testBridgePlacement()
	before.Index = types.Int64Value(0)
	desired := before
	desired.Index = types.Int64Value(1)
	state := groupGraphModel{ParentPresent: types.BoolValue(true), ParentPlacement: &before}
	plan := groupGraphModel{ParentPlacement: &desired}
	observed := bridgeGroupObservation{Placement: desired}
	if err := validateGroupGraphPreMutationSnapshot(observed, state, plan); err != nil {
		t.Fatal(err)
	}
	if err := validateGroupGraphPreMutationSnapshot(observed, state); err == nil {
		t.Fatal("index shift accepted without plan")
	}
	unexpected := observed
	unexpected.Placement.Index = types.Int64Value(2)
	if err := validateGroupGraphPreMutationSnapshot(unexpected, state, plan); err == nil {
		t.Fatal("unexpected index accepted")
	}
	changed := observed
	tr := *changed.Placement.Transform
	tr.X = types.Float64Value(tr.X.ValueFloat64() + 10)
	changed.Placement.Transform = &tr
	if err := validateGroupGraphPreMutationSnapshot(changed, state, plan); err == nil {
		t.Fatal("index convergence masked concurrent transform change")
	}
	unknown := desired
	unknown.Index = types.Int64Unknown()
	if err := validateGroupGraphPreMutationSnapshot(observed, state, groupGraphModel{ParentPlacement: &unknown}); err == nil {
		t.Fatal("unknown desired index accepted")
	}
}
