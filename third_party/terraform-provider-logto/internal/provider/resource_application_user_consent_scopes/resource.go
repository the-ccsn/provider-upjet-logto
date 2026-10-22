package resource_application_user_consent_scopes

import (
	"context"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type consentResource struct{ client *client.Client }
type model struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	UserScopes    types.Set    `tfsdk:"user_scopes"`
}

var _ resource.ResourceWithImportState = (*consentResource)(nil)
var _ resource.ResourceWithConfigure = (*consentResource)(nil)
var _ resource.ResourceWithValidateConfig = (*consentResource)(nil)

func New() resource.Resource { return &consentResource{} }
func (r *consentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_user_consent_scopes"
}
func (r *consentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Authoritative user profile consent scopes for one application. Does not manage API or organization consent scopes. Import uses the application ID; only one resource may manage an application's user scopes.", Attributes: map[string]schema.Attribute{
		"id":             schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"application_id": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"user_scopes":    schema.SetAttribute{Required: true, ElementType: types.StringType, Description: "Allowed user profile scopes, for example profile and email. An empty set revokes all user profile scopes. Do not include openid, which is implicit."},
	}}
}
func validateUserScopes(scopes types.Set) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if scopes.IsNull() {
		diagnostics.AddAttributeError(path.Root("user_scopes"), "Missing user consent scopes", "user_scopes must be configured. Use an explicit empty set to revoke all user profile scopes.")
		return diagnostics
	}
	for _, value := range scopes.Elements() {
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		scope := value.(types.String).ValueString()
		if !client.ValidUserConsentScope(scope) {
			diagnostics.AddAttributeError(path.Root("user_scopes"), "Invalid user consent scope", "Unsupported user consent scope: "+scope)
		}
	}
	return diagnostics
}

func (r *consentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m model
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.ApplicationID.IsNull() && !m.ApplicationID.IsUnknown() && !client.ValidApplicationID(m.ApplicationID.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("application_id"), "Invalid application ID", "Expected a nonempty application ID without path separators, URL delimiters, or control characters.")
	}
	resp.Diagnostics.Append(validateUserScopes(m.UserScopes)...)
}
func (r *consentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Logto API client.")
	}
}
func (r *consentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Framework callers may apply without a separate config validation RPC.
	// An unreadable ID must never enter the partial state used for recovery.
	if !client.ValidApplicationID(m.ApplicationID.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("application_id"), "Invalid application ID", "Expected a nonempty application ID without path separators, URL delimiters, or control characters.")
		return
	}
	var scopes []string
	resp.Diagnostics.Append(m.UserScopes.ElementsAs(ctx, &scopes, false)...)
	resp.Diagnostics.Append(validateUserScopes(m.UserScopes)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The identity is deterministic. Preserve it even if a secondary mutation fails,
	// allowing Terraform and Upjet to refresh and resume partial reconciliation.
	m.ID = m.ApplicationID
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplicationUserConsentScopesUpdate(ctx, m.ApplicationID.ValueString(), scopes); err != nil {
		resp.Diagnostics.AddError("Error assigning application user consent scopes", err.Error())
	}
}
func (r *consentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m model
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.ID.IsNull() || m.ID.IsUnknown() || m.ID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	scopes, err := r.client.ApplicationUserConsentScopesGet(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading application user consent scopes", err.Error())
		return
	}
	if scopes == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	m.ApplicationID = m.ID
	values, diags := types.SetValueFrom(ctx, types.StringType, scopes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.UserScopes = values
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *consentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var scopes []string
	resp.Diagnostics.Append(m.UserScopes.ElementsAs(ctx, &scopes, false)...)
	resp.Diagnostics.Append(validateUserScopes(m.UserScopes)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ApplicationUserConsentScopesUpdate(ctx, m.ApplicationID.ValueString(), scopes); err != nil {
		resp.Diagnostics.AddError("Error updating application user consent scopes", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *consentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m model
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A missing parent means there are no grants left to revoke.
	scopes, err := r.client.ApplicationUserConsentScopesGet(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading application user consent scopes", err.Error())
		return
	}
	if scopes == nil {
		return
	}
	if err := r.client.ApplicationUserConsentScopesUpdate(ctx, m.ID.ValueString(), []string{}); err != nil {
		resp.Diagnostics.AddError("Error revoking application user consent scopes", err.Error())
	}
}
func (r *consentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !client.ValidApplicationID(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected an application ID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("application_id"), req.ID)...)
}
