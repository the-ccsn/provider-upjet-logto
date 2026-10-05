package resource_configuration

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/Lenstra/terraform-provider-logto/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type configurationResource struct {
	kind   string
	client *client.Client
}

var _ resource.ResourceWithImportState = (*configurationResource)(nil)
var _ resource.ResourceWithConfigure = (*configurationResource)(nil)
var _ resource.ResourceWithValidateConfig = (*configurationResource)(nil)

func New(kind string) func() resource.Resource {
	return func() resource.Resource { return &configurationResource{kind: kind} }
}

func (r *configurationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind
}

func (r *configurationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	d, _ := client.ConfigurationContract(r.kind)
	resp.Schema = schema.Schema{Description: "Explicit Logto configuration contract. JSON contains only managed fields. Singleton import ID is default; deletion releases ownership without resetting tenant settings. Connector import ID is its API ID.", Attributes: map[string]schema.Attribute{
		"id":            schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"configuration": schema.StringAttribute{Required: true, Sensitive: d.Sensitive, Description: "JSON object with the managed API fields; omitted fields remain unmanaged."},
	}}
	if r.kind == "connector" {
		resp.Schema.Attributes["connector_id"] = schema.StringAttribute{Required: true, Description: "Connector factory ID, e.g. github-universal. Changing the factory requires replacement.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
}

func (r *configurationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider client", "Expected a Logto API client.")
	}
}

func (r *configurationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var value types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("configuration"), &value)...)
	if !value.IsNull() && !value.IsUnknown() {
		if _, err := client.ValidateConfiguration(r.kind, value.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("configuration"), "Invalid configuration", err.Error())
		}
	}
}

type attributeGetter interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}
type attributeSetter interface {
	SetAttribute(context.Context, path.Path, any) diag.Diagnostics
}
type configurationModel struct{ id, configuration, connectorID types.String }

func (r *configurationResource) readModel(ctx context.Context, from attributeGetter) (configurationModel, diag.Diagnostics) {
	var m configurationModel
	d := from.GetAttribute(ctx, path.Root("id"), &m.id)
	d.Append(from.GetAttribute(ctx, path.Root("configuration"), &m.configuration)...)
	if r.kind == "connector" {
		d.Append(from.GetAttribute(ctx, path.Root("connector_id"), &m.connectorID)...)
	}
	return m, d
}

func (r *configurationResource) writeModel(ctx context.Context, to attributeSetter, m configurationModel) diag.Diagnostics {
	d := to.SetAttribute(ctx, path.Root("id"), m.id)
	d.Append(to.SetAttribute(ctx, path.Root("configuration"), m.configuration)...)
	if r.kind == "connector" {
		d.Append(to.SetAttribute(ctx, path.Root("connector_id"), m.connectorID)...)
	}
	return d
}

// Project the response recursively to managed JSON keys. Unknown server fields stay unmanaged.
func project(actual, desired any) any {
	keys, ok := desired.(map[string]any)
	if !ok {
		return actual
	}
	remote, ok := actual.(map[string]any)
	if !ok {
		return actual
	}
	result := map[string]any{}
	for key, value := range keys {
		if found, ok := remote[key]; ok {
			result[key] = project(found, value)
		}
	}
	return result
}

func (r *configurationResource) observe(m configurationModel, remote map[string]any) configurationModel {
	d, _ := client.ConfigurationContract(r.kind)
	filtered := map[string]any{}
	for _, key := range d.Fields {
		if v, ok := remote[key]; ok {
			filtered[key] = v
		}
	}
	var desired map[string]any
	if !m.configuration.IsNull() && !m.configuration.IsUnknown() && json.Unmarshal([]byte(m.configuration.ValueString()), &desired) == nil && desired != nil {
		actual := project(filtered, desired)
		if !reflect.DeepEqual(actual, desired) {
			encoded, _ := json.Marshal(actual)
			m.configuration = types.StringValue(string(encoded))
		}
	} else {
		encoded, _ := json.Marshal(filtered)
		m.configuration = types.StringValue(string(encoded))
	}
	if r.kind == "connector" {
		if factory, ok := remote["connectorId"].(string); ok {
			m.connectorID = types.StringValue(factory)
		}
	}
	return m
}

func (r *configurationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	m, d := r.readModel(ctx, req.Plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := "default"
	remote, err := r.client.ConfigurationWrite(ctx, r.kind, id, m.connectorID.ValueString(), m.configuration.ValueString(), true)
	if err != nil {
		resp.Diagnostics.AddError("Error applying Logto configuration", err.Error())
		return
	}
	if r.kind == "connector" {
		var ok bool
		id, ok = remote["id"].(string)
		if !ok || id == "" {
			resp.Diagnostics.AddError("Missing connector ID", "Logto did not return an ID; inspect the server before retrying creation.")
			return
		}
	}
	m.id = types.StringValue(id)
	resp.Diagnostics.Append(r.writeModel(ctx, &resp.State, m)...)
}

func (r *configurationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	m, d := r.readModel(ctx, req.State)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.id.IsNull() || m.id.IsUnknown() || m.id.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	remote, err := r.client.ConfigurationGet(ctx, r.kind, m.id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading Logto configuration", err.Error())
		return
	}
	if remote == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(r.writeModel(ctx, &resp.State, r.observe(m, remote))...)
}

func (r *configurationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	m, d := r.readModel(ctx, req.Plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.ConfigurationWrite(ctx, r.kind, m.id.ValueString(), m.connectorID.ValueString(), m.configuration.ValueString(), false); err != nil {
		resp.Diagnostics.AddError("Error updating Logto configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(r.writeModel(ctx, &resp.State, m)...)
}

func (r *configurationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	m, d := r.readModel(ctx, req.State)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ConfigurationDelete(ctx, r.kind, m.id.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error deleting Logto configuration", err.Error())
	}
}

func (r *configurationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.kind != "connector" && req.ID != "default" {
		resp.Diagnostics.AddError("Invalid import ID", "Singleton configuration ID must be default.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
