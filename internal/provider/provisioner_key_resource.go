package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/coder/coder/v2/codersdk"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ProvisionerKeyResource{}

func NewProvisionerKeyResource() resource.Resource {
	return &ProvisionerKeyResource{}
}

// ProvisionerKeyResource defines the resource implementation.
type ProvisionerKeyResource struct {
	*CoderdProviderData
}

// ProvisionerKeyResourceModel describes the resource data model.
type ProvisionerKeyResourceModel struct {
	OrganizationID UUID         `tfsdk:"organization_id"`
	Name           types.String `tfsdk:"name"`
	Tags           types.Map    `tfsdk:"tags"`
	Key            types.String `tfsdk:"key"`
}

func (r *ProvisionerKeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_provisioner_key"
}

func (r *ProvisionerKeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A provisioner key for a Coder deployment.",

		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				CustomType:          UUIDType,
				MarkdownDescription: "The organization that provisioners connected with this key will be connected to.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the key.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tags": schema.MapAttribute{
				ElementType:         types.StringType,
				MarkdownDescription: "The tags that the provisioner will accept jobs for.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "A provisionerkey key for Coder.",
				Computed:            true,
				Sensitive:           true,
			},
		},
	}
}

func (r *ProvisionerKeyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*CoderdProviderData)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.CoderdProviderData = data
}

func (r *ProvisionerKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read Terraform plan data into the model
	var data ProvisionerKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createKeyResult, err := r.Client.CreateProvisionerKey(ctx, data.OrganizationID.ValueUUID(), codersdk.CreateProvisionerKeyRequest{
		Name: data.Name.ValueString(),
		Tags: map[string]string{},
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create provisioner_key, got error: %s", err))
		return
	}

	data.Key = types.StringValue(createKeyResult.Key)
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProvisionerKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read Terraform prior state data into the model
	var data ProvisionerKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Provisioner keys are immutable, no reading necessary.

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProvisionerKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Provisioner keys are immutable, updating is always invalid.
	resp.Diagnostics.Append(diag.NewErrorDiagnostic("invalid update", "terraform is attempting to update a resource which must be replaced"))
}

func (r *ProvisionerKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Read Terraform prior state data into the model
	var data ProvisionerKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.Client.DeleteProvisionerKey(ctx, data.OrganizationID.ValueUUID(), data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete provisionerkey, got error: %s", err))
		return
	}
}
