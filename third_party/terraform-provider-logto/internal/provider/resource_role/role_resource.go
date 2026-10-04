package resource_role

import (
	"context"
	"sort"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, state RoleModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	role := decodePlan(ctx, plan)

	role, err := r.client.RoleCreate(ctx, role)
	if err != nil {
		resp.Diagnostics.AddError("Error creating role", err.Error())
		return
	}

	// Persist the identifier before the secondary request so failures cannot orphan the role.
	role.ScopeIds = decodePlan(ctx, plan).ScopeIds
	resp.Diagnostics.Append(convertToTerraformModel(ctx, role, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleScopes, err := r.client.RoleScopesGet(ctx, role.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error fetching roleScopes just after role creation", err.Error())
		return
	}

	role.ScopeIds = nil
	if !plan.ScopeIds.IsNull() {
		role.ScopeIds = orderedScopeIDs(roleScopes, decodePlan(ctx, plan).ScopeIds)
	}

	diags = convertToTerraformModel(ctx, role, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state RoleModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Upjet observes a new resource before creation with an empty identifier.
	// Treat only that uninitialized state as absent; malformed IDs still fail.
	if state.Id.IsNull() || state.Id.IsUnknown() || state.Id.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	role, err := r.client.RoleGet(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading role", err.Error())
		return
	}
	if role == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	roleScopes, err := r.client.RoleScopesGet(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading role scopes", err.Error())
		return
	}

	if state.ScopeIds.IsNull() {
		role.ScopeIds = nil
	} else {
		var previous []string
		resp.Diagnostics.Append(state.ScopeIds.ElementsAs(ctx, &previous, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		role.ScopeIds = orderedScopeIDs(roleScopes, previous)
	}

	diags = convertToTerraformModel(ctx, role, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state RoleModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	role := decodePlan(ctx, plan)

	role, err := r.client.RoleUpdate(ctx, role)
	if err != nil {
		resp.Diagnostics.AddError("Error updating role", err.Error())
		return
	}

	// PATCH returns role fields only; reconcile relation endpoints without replacing the role.
	role.ScopeIds = decodePlan(ctx, plan).ScopeIds
	if !plan.ScopeIds.IsNull() && !plan.ScopeIds.IsUnknown() {
		if err := r.client.RoleScopesUpdate(ctx, role.ID, role.ScopeIds); err != nil {
			resp.Diagnostics.AddError("Error updating role scopes", err.Error())
			return
		}
	}

	diags = convertToTerraformModel(ctx, role, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state RoleModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RoleDelete(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting role", err.Error())
	}
}

func decodePlan(ctx context.Context, plan RoleModel) *client.RoleModel {
	model := &client.RoleModel{
		ID:          plan.Id.ValueString(),
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	}

	if !plan.Type.IsNull() && !plan.Type.IsUnknown() {
		model.Type = plan.Type.ValueString()
	}

	if !plan.IsDefault.IsNull() && !plan.IsDefault.IsUnknown() {
		model.IsDefault = plan.IsDefault.ValueBool()
	}

	if !plan.ScopeIds.IsNull() && !plan.ScopeIds.IsUnknown() {
		model.ScopeIds = []string{}
		plan.ScopeIds.ElementsAs(ctx, &model.ScopeIds, true)
	}

	return model
}

func convertToTerraformModel(ctx context.Context, role *client.RoleModel, model *RoleModel) (diags diag.Diagnostics) {
	*model = RoleModel{
		Id:          types.StringValue(role.ID),
		Name:        types.StringValue(role.Name),
		Description: types.StringValue(role.Description),
		Type:        types.StringValue(role.Type),
		IsDefault:   types.BoolValue(role.IsDefault),
	}

	if role.ScopeIds == nil {
		model.ScopeIds = types.ListNull(types.StringType)
	} else {
		model.ScopeIds, diags = convertList(ctx, types.StringType, role.ScopeIds)
		if diags.HasError() {
			return
		}
	}

	return
}

func convertList[E any](ctx context.Context, elementType attr.Type, list []E) (basetypes.ListValue, diag.Diagnostics) {
	return basetypes.NewListValueFrom(ctx, elementType, list)
}

// Relation APIs return an unordered collection; Terraform lists must retain plan order.
func orderedScopeIDs(scopes []client.ScopeModel, preferred []string) []string {
	remaining := map[string]bool{}
	for _, scope := range scopes {
		remaining[scope.ID] = true
	}
	result := make([]string, 0, len(remaining))
	for _, id := range preferred {
		if remaining[id] {
			result = append(result, id)
			delete(remaining, id)
		}
	}
	extra := make([]string, 0, len(remaining))
	for id := range remaining {
		extra = append(extra, id)
	}
	sort.Strings(extra)
	return append(result, extra...)
}
