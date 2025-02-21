package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &OrganizationSyncSettingsResource{}

type OrganizationSyncSettingsResource struct {
	*CoderdProviderData
}

// OrganizationSyncSettingsResourceModel describes the resource data model.
type OrganizationSyncSettingsResourceModel struct {
	Field         types.String `tfsdk:"field"`
	AssignDefault types.Bool   `tfsdk:"assign_default"`
	Mapping       types.Map    `tfsdk:"mapping"`
}

func NewOrganizationSyncSettingsResource() resource.Resource {
	return &OrganizationSyncSettingsResource{}
}

func (r *OrganizationSyncSettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_sync_settings"
}

func (r *OrganizationSyncSettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `IdP sync settings for organizations.

This resource can only be created once. Attempts to create multiple will fail.

~> **Warning**
This resource is only compatible with Coder version [2.19.0](https://github.com/coder/coder/releases/tag/v2.19.0) and later.
`,
		Attributes: map[string]schema.Attribute{
			"field": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The claim field that specifies what organizations " +
					"a user should be in.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"assign_default": schema.BoolAttribute{
				Required: true,
				MarkdownDescription: "When true, every user will be added to the default " +
					"organization, regardless of claims.",
			},
			"mapping": schema.MapAttribute{
				ElementType:         types.ListType{ElemType: UUIDType},
				Optional:            true,
				MarkdownDescription: "A map from OIDC group name to Coder organization ID.",
			},
		},
	}
}

func (r *OrganizationSyncSettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*CoderdProviderData)

	if !ok {
		resp.Diagnostics.AddError(
			"Unable to configure provider data",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.CoderdProviderData = data
}

func (r *OrganizationSyncSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read Terraform prior state data into the model
	var data OrganizationSyncSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	settings, err := r.Client.OrganizationIDPSyncSettings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("unable to get organization sync settings, got error: %s", err))
		return
	}

	// Store the latest values that we just fetched.
	data.Field = types.StringValue(settings.Field)
	data.AssignDefault = types.BoolValue(settings.AssignDefault)

	if !data.Mapping.IsNull() {
		// Convert IDs to strings
		elements := make(map[string][]string)
		for key, ids := range settings.Mapping {
			for _, id := range ids {
				elements[key] = append(elements[key], id.String())
			}
		}

		mapping, diags := types.MapValueFrom(ctx, types.ListType{ElemType: UUIDType}, elements)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.Mapping = mapping
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationSyncSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read Terraform plan data into the model
	var data OrganizationSyncSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "creating organization sync", map[string]any{
		"field":          data.Field.ValueString(),
		"assign_default": data.AssignDefault.ValueBool(),
	})

	// Create and Update use a shared implementation
	resp.Diagnostics.Append(r.patch(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully created organization sync", map[string]any{
		"field":          data.Field.ValueString(),
		"assign_default": data.AssignDefault.ValueBool(),
	})

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationSyncSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Read Terraform plan data into the model
	var data OrganizationSyncSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Update the organization metadata
	tflog.Trace(ctx, "updating organization", map[string]any{
		"field":          data.Field.ValueString(),
		"assign_default": data.AssignDefault.ValueBool(),
	})

	// Create and Update use a shared implementation
	resp.Diagnostics.Append(r.patch(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully updated organization", map[string]any{
		"field":          data.Field.ValueString(),
		"assign_default": data.AssignDefault.ValueBool(),
	})

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationSyncSettingsResource) patch(
	ctx context.Context,
	data OrganizationSyncSettingsResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics
	field := data.Field.ValueString()
	assignDefault := data.AssignDefault.ValueBool()

	if data.Mapping.IsNull() {
		_, err := r.Client.PatchOrganizationIDPSyncConfig(ctx, codersdk.PatchOrganizationIDPSyncConfigRequest{
			Field:         field,
			AssignDefault: assignDefault,
		})

		if err != nil {
			diags.AddError("failed to create organization sync", err.Error())
			return diags
		}
	} else {
		settings := codersdk.OrganizationSyncSettings{
			Field:         field,
			AssignDefault: assignDefault,
			Mapping:       map[string][]uuid.UUID{},
		}

		// Terraform doesn't know how to turn one our `UUID` Terraform values into a
		// `uuid.UUID`, so we have to do the unwrapping manually here.
		var mapping map[string][]UUID
		diags.Append(data.Mapping.ElementsAs(ctx, &mapping, false)...)
		if diags.HasError() {
			return diags
		}
		for key, ids := range mapping {
			for _, id := range ids {
				settings.Mapping[key] = append(settings.Mapping[key], id.ValueUUID())
			}
		}

		_, err := r.Client.PatchOrganizationIDPSyncSettings(ctx, settings)
		if err != nil {
			diags.AddError("failed to create organization sync", err.Error())
			return diags
		}
	}

	return diags
}

func (r *OrganizationSyncSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Read Terraform prior state data into the model
	var data OrganizationSyncSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "deleting organization sync", map[string]any{})
	_, err := r.Client.PatchOrganizationIDPSyncConfig(ctx, codersdk.PatchOrganizationIDPSyncConfigRequest{
		// This disables organization sync without causing state conflicts for
		// organization resources that might still specify `org_sync_idp_groups`.
		Field: "",
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("unable to delete organization sync, got error: %s", err))
		return
	}
	tflog.Trace(ctx, "successfully deleted organization sync", map[string]any{})

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
}
