package resource_application

import (
	"context"
	"encoding/json"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"reflect"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func (r *applicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, state ApplicationModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	application, metadataDiags := decodePlan(ctx, plan)
	resp.Diagnostics.Append(metadataDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	application, err := r.client.ApplicationCreate(ctx, application)
	if err != nil {
		resp.Diagnostics.AddError("Error creating application", err.Error())
		return
	}

	state = plan
	diags = convertToTerraformModel(ctx, application, &state)
	// Retain the created ID even if credential retrieval fails on a later request.
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !diags.HasError() {
		resp.Diagnostics.Append(r.readSecrets(ctx, application, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

func (r *applicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ApplicationModel
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

	application, err := r.client.ApplicationGet(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading application", err.Error())
		return
	}

	if application == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	diags = convertToTerraformModel(ctx, application, &state)
	if !diags.HasError() {
		resp.Diagnostics.Append(r.readSecrets(ctx, application, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

func (r *applicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ApplicationModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	application, metadataDiags := decodePlan(ctx, plan)
	resp.Diagnostics.Append(metadataDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	application, err := r.client.ApplicationUpdate(ctx, application)
	if err != nil {
		resp.Diagnostics.AddError("Error updating application", err.Error())
		return
	}

	state = plan
	diags = convertToTerraformModel(ctx, application, &state)
	if !diags.HasError() {
		resp.Diagnostics.Append(r.readSecrets(ctx, application, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

func (r *applicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ApplicationModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.ApplicationDelete(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting application", err.Error())
	}
}

func decodePlan(ctx context.Context, plan ApplicationModel) (*client.ApplicationModel, diag.Diagnostics) {
	var d diag.Diagnostics
	model := &client.ApplicationModel{
		ID:                 plan.Id.ValueString(),
		Name:               plan.Name.ValueString(),
		Type:               plan.Type.ValueString(),
		Description:        plan.Description.ValueString(),
		IsThirdParty:       plan.IsThirdParty.ValueBool(),
		OidcClientMetadata: &client.OidcClientMetadata{},
	}
	plan.RedirectUris.ElementsAs(ctx, &model.OidcClientMetadata.RedirectUris, true)
	plan.PostLogoutRedirectUris.ElementsAs(ctx, &model.OidcClientMetadata.PostLogoutRedirectUris, true)

	if !plan.CorsAllowedOrigins.IsNull() {
		model.CustomClientMetadata = &client.CustomClientMetadata{}
		plan.CorsAllowedOrigins.ElementsAs(ctx, &model.CustomClientMetadata.CorsAllowedOrigins, true)
	}

	if !plan.OidcClientMetadataExtra.IsNull() && !plan.OidcClientMetadataExtra.IsUnknown() {
		var err error
		model.OidcClientMetadataExtra, err = client.ValidateApplicationMetadata("oidcClientMetadata", plan.OidcClientMetadataExtra.ValueString())
		if err != nil {
			d.AddError("Invalid OIDC metadata", err.Error())
		}
	}
	if !plan.CustomClientMetadataExtra.IsNull() && !plan.CustomClientMetadataExtra.IsUnknown() {
		var err error
		model.CustomClientMetadataExtra, err = client.ValidateApplicationMetadata("customClientMetadata", plan.CustomClientMetadataExtra.ValueString())
		if err != nil {
			d.AddError("Invalid custom metadata", err.Error())
		}
	}
	return model, d
}

func convertToTerraformModel(ctx context.Context, app *client.ApplicationModel, model *ApplicationModel) (diags diag.Diagnostics) {
	oidcExtra := metadataJSONState(model.OidcClientMetadataExtra, app.OidcClientMetadataExtra)
	customExtra := metadataJSONState(model.CustomClientMetadataExtra, app.CustomClientMetadataExtra)
	*model = ApplicationModel{
		OidcClientMetadataExtra:   oidcExtra,
		CustomClientMetadataExtra: customExtra,
		ClientSecrets:             types.MapNull(types.StringType),
		Id:                        types.StringValue(app.ID),
		TenantId:                  types.StringValue(app.TenantId),
		Name:                      types.StringValue(app.Name),
		Description:               types.StringValue(app.Description),
		Type:                      types.StringValue(app.Type),
		IsThirdParty:              types.BoolValue(app.IsThirdParty),
		IsAdmin:                   types.BoolValue(app.IsAdmin),
	}

	if app.OidcClientMetadata != nil {
		model.RedirectUris, diags = convertList(ctx, types.StringType, app.OidcClientMetadata.RedirectUris)
		if diags.HasError() {
			return
		}
		model.PostLogoutRedirectUris, diags = convertList(ctx, types.StringType, app.OidcClientMetadata.PostLogoutRedirectUris)
		if diags.HasError() {
			return
		}
	}

	var corsAllowedOrigins []string
	if app.CustomClientMetadata != nil {
		corsAllowedOrigins = app.CustomClientMetadata.CorsAllowedOrigins
	}
	model.CorsAllowedOrigins, diags = convertList(ctx, types.StringType, corsAllowedOrigins)
	if diags.HasError() {
		return
	}

	return
}

func convertList[E any](ctx context.Context, elementType attr.Type, list []E) (basetypes.ListValue, diag.Diagnostics) {
	if len(list) == 0 {
		return basetypes.NewListValueFrom(ctx, elementType, []attr.Value{})
	}
	return basetypes.NewListValueFrom(ctx, elementType, list)
}

// readSecrets reads existing credentials; public clients do not have secrets.
func (r *applicationResource) readSecrets(ctx context.Context, app *client.ApplicationModel, model *ApplicationModel) diag.Diagnostics {
	values := map[string]string{}
	if app.Type != "Native" && app.Type != "SPA" {
		secrets, err := r.client.GetApplicationSecrets(ctx, app.ID)
		if err != nil {
			var d diag.Diagnostics
			d.AddError("Error reading application secrets", err.Error())
			return d
		}
		for _, secret := range secrets {
			values[secret.Name] = secret.Value
		}
	}
	var d diag.Diagnostics
	model.ClientSecrets, d = types.MapValueFrom(ctx, types.StringType, values)
	return d
}

func metadataJSONState(prior types.String, actual map[string]any) types.String {
	if actual == nil {
		actual = map[string]any{}
	}
	var desired map[string]any
	if !prior.IsNull() && !prior.IsUnknown() && json.Unmarshal([]byte(prior.ValueString()), &desired) == nil && desired != nil {
		projected := map[string]any{}
		for key := range desired {
			if value, ok := actual[key]; ok {
				projected[key] = value
			}
		}
		if reflect.DeepEqual(desired, projected) {
			return prior
		}
		actual = projected
	}
	encoded, _ := json.Marshal(actual)
	return types.StringValue(string(encoded))
}

func (r *applicationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	for attribute, kind := range map[string]string{"oidc_client_metadata_extra": "oidcClientMetadata", "custom_client_metadata_extra": "customClientMetadata"} {
		var value types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attribute), &value)...)
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		if _, err := client.ValidateApplicationMetadata(kind, value.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(attribute), "Invalid application metadata", err.Error())
		}
	}
}
