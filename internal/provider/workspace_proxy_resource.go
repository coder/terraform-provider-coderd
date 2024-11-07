package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &WorkspaceProxyResource{}

func NewWorkspaceProxyResource() resource.Resource {
	return &WorkspaceProxyResource{}
}

// WorkspaceProxyResource defines the resource implementation.
type WorkspaceProxyResource struct {
	data *CoderdProviderData
}

// WorkspaceProxyResourceModel describes the resource data model.
type WorkspaceProxyResourceModel struct {
	ID           UUID         `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	DisplayName  types.String `tfsdk:"display_name"`
	Icon         types.String `tfsdk:"icon"`
	SessionToken types.String `tfsdk:"session_token"`
}

func (r *WorkspaceProxyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_proxy"
}

func (r *WorkspaceProxyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Workspace Proxy for the Coder deployment.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				CustomType:          UUIDType,
				Computed:            true,
				MarkdownDescription: "Workspace Proxy ID",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the workspace proxy.",
				Required:            true,
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the workspace proxy.",
				Optional:            true,
				Computed:            true,
			},
			"icon": schema.StringAttribute{
				MarkdownDescription: "Relative path or external URL that specifies an icon to be displayed in the dashboard.",
				Required:            true,
			},
			"session_token": schema.StringAttribute{
				MarkdownDescription: "Session token for the workspace proxy.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *WorkspaceProxyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *WorkspaceProxyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data WorkspaceProxyResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.data.Features[codersdk.FeatureWorkspaceProxy].Enabled {
		resp.Diagnostics.AddError("Feature not enabled", "Your license is not entitled to create workspace proxies.")
		return
	}

	client := r.data.Client
	wsp, err := client.CreateWorkspaceProxy(ctx, codersdk.CreateWorkspaceProxyRequest{
		Name:        data.Name.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
		Icon:        data.Icon.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to create workspace proxy: %v", err))
		return
	}

	data.ID = UUIDValue(wsp.Proxy.ID)
	data.Name = types.StringValue(wsp.Proxy.Name)
	data.DisplayName = types.StringValue(wsp.Proxy.DisplayName)
	data.Icon = types.StringValue(wsp.Proxy.IconURL)
	data.SessionToken = types.StringValue(wsp.ProxyToken)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkspaceProxyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data WorkspaceProxyResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	wsp, err := client.WorkspaceProxyByID(ctx, data.ID.ValueUUID())
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Workspace proxy with ID %s not found. Marking as deleted.", data.ID.ValueString()))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to read workspace proxy: %v", err))
		return
	}

	data.ID = UUIDValue(wsp.ID)
	data.Name = types.StringValue(wsp.Name)
	data.DisplayName = types.StringValue(wsp.DisplayName)
	data.Icon = types.StringValue(wsp.IconURL)

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkspaceProxyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data WorkspaceProxyResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	wsp, err := client.PatchWorkspaceProxy(ctx, codersdk.PatchWorkspaceProxy{
		ID:              data.ID.ValueUUID(),
		Name:            data.Name.ValueString(),
		DisplayName:     data.DisplayName.ValueString(),
		Icon:            data.Icon.ValueString(),
		RegenerateToken: false,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update workspace proxy: %v", err))
		return
	}

	data.Name = types.StringValue(wsp.Proxy.Name)
	data.DisplayName = types.StringValue(wsp.Proxy.DisplayName)
	data.Icon = types.StringValue(wsp.Proxy.IconURL)

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkspaceProxyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data WorkspaceProxyResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	err := client.DeleteWorkspaceProxyByID(ctx, data.ID.ValueUUID())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to delete workspace proxy: %v", err))
		return
	}
}
