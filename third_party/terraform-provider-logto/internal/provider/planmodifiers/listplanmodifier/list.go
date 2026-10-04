package listplanmodifier

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func NullIsEmpty() planmodifier.List {
	return nullIsEmptyModifier{}
}

// nullIsEmptyModifier implements the plan modifier.
type nullIsEmptyModifier struct{}

// Description returns a human-readable description of the plan modifier.
func (m nullIsEmptyModifier) Description(_ context.Context) string {
	return "Omitted lists preserve prior state; a new omitted list defaults to empty."
}

// MarkdownDescription returns a markdown description of the plan modifier.
func (m nullIsEmptyModifier) MarkdownDescription(_ context.Context) string {
	return "Omitted lists preserve prior state; a new omitted list defaults to empty."
}

// PlanModifyList implements the plan modification logic.
func (m nullIsEmptyModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.ConfigValue.IsNull() {
		if !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
			resp.PlanValue = req.StateValue
		} else {
			resp.PlanValue = types.ListValueMust(req.ConfigValue.ElementType(ctx), nil)
		}
	}
}
