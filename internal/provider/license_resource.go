package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/coder/coder/v2/codersdk"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &LicenseResource{}

func NewLicenseResource() resource.Resource {
	return &LicenseResource{}
}

// LicenseResource defines the resource implementation.
type LicenseResource struct {
	data *CoderdProviderData
}

// LicenseResourceModel describes the resource data model.
type LicenseResourceModel struct {
	ID        types.Int32  `tfsdk:"id"`
	ExpiresAt types.Int64  `tfsdk:"expires_at"`
	License   types.String `tfsdk:"license"`
}

func (r *LicenseResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_license"
}

func (r *LicenseResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A license for a Coder deployment.\n\nIt's recommended to set " +
			"[`create_before_destroy`](https://developer.hashicorp.com/terraform/language/meta-arguments/lifecycle#create_before_destroy) " +
			"on license resources. Without setting this, Terraform will remove the old " +
			"license before adding the updated license. This will result in a temporary " +
			"disruption to your users; during which they may be unable to use features " +
			"that require a license.\n\n" +
			"Terraform does not guarantee this resource " +
			"will be created before other resources or attributes that require a licensed deployment. " +
			"The `depends_on` meta-argument is instead recommended.",

		Attributes: map[string]schema.Attribute{
			"id": schema.Int32Attribute{
				MarkdownDescription: "Integer ID of the license.",
				Computed:            true,
				PlanModifiers: []planmodifier.Int32{
					int32planmodifier.UseStateForUnknown(),
				},
			},
			"expires_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp of when the license expires.",
				Computed:            true,
			},
			"license": schema.StringAttribute{
				MarkdownDescription: "A license key for Coder.",
				Required:            true,
				Sensitive:           true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *LicenseResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	r.data = data
}

func (r *LicenseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data LicenseResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	license, err := client.AddLicense(ctx, codersdk.AddLicenseRequest{
		License: data.License.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add license, got error: %s", err))
		return
	}
	data.ID = types.Int32Value(license.ID)
	expiresAt, err := license.ExpiresAt()
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to parse license expiration, got error: %s", err))
		return
	}
	data.ExpiresAt = types.Int64Value(expiresAt.Unix())

	entitlements, err := client.Entitlements(ctx)
	if err != nil {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Unable to refresh deployment entitlements after adding license, got error: %s", err))
	} else {
		r.data.SetFeatures(entitlements.Features)
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *LicenseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data LicenseResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	licenses, err := r.data.Client.Licenses(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list licenses, got error: %s", err))
		return
	}

	found := false
	for _, license := range licenses {
		if license.ID == data.ID.ValueInt32() {
			found = true
			expiresAt, err := license.ExpiresAt()
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to parse license expiration, got error: %s", err))
				return
			}
			data.ExpiresAt = types.Int64Value(expiresAt.Unix())
		}
	}
	if !found {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("License with ID %d not found. Marking as deleted.", data.ID.ValueInt32()))
		resp.State.RemoveResource(ctx)
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *LicenseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data LicenseResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Update is handled by replacement

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *LicenseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data LicenseResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	err := client.DeleteLicense(ctx, data.ID.ValueInt32())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete license, got error: %s", err))
		return
	}

	entitlements, err := client.Entitlements(ctx)
	if err != nil {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Unable to refresh deployment entitlements after deleting license, got error: %s", err))
	} else {
		r.data.SetFeatures(entitlements.Features)
	}
}
