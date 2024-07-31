package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/coder/coder/v2/codersdk"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &UserResource{}
var _ resource.ResourceWithImportState = &UserResource{}

func NewUserResource() resource.Resource {
	return &UserResource{}
}

// UserResource defines the resource implementation.
type UserResource struct {
	data *CoderdProviderData
}

// UserResourceModel describes the resource data model.
type UserResourceModel struct {
	ID UUID `tfsdk:"id"`

	Username  types.String `tfsdk:"username"`
	Name      types.String `tfsdk:"name"`
	Email     types.String `tfsdk:"email"`
	Roles     types.Set    `tfsdk:"roles"`      // owner, template-admin, user-admin, auditor (member is implicit)
	LoginType types.String `tfsdk:"login_type"` // none, password, github, oidc
	Password  types.String `tfsdk:"password"`   // only when login_type is password
	Suspended types.Bool   `tfsdk:"suspended"`
}

func (r *UserResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *UserResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A user on the Coder deployment.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				CustomType:          UUIDType,
				Computed:            true,
				MarkdownDescription: "User ID",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Username of the user.",
				Required:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name of the user. Defaults to username.",
				Computed:            true,
				Optional:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Email address of the user.",
				Required:            true,
			},
			"roles": schema.SetAttribute{
				MarkdownDescription: "Roles assigned to the user. Valid roles are 'owner', 'template-admin', 'user-admin', and 'auditor'.",
				Computed:            true,
				Optional:            true,
				ElementType:         types.StringType,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(
						stringvalidator.OneOf("owner", "template-admin", "user-admin", "auditor"),
					),
				},
				Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			},
			"login_type": schema.StringAttribute{
				MarkdownDescription: "Type of login for the user. Valid types are 'none', 'password', 'github', and 'oidc'.",
				Computed:            true,
				Optional:            true,
				Validators: []validator.String{
					stringvalidator.OneOf("none", "password", "github", "oidc"),
				},
				Default: stringdefault.StaticString("none"),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password for the user. Required when login_type is 'password'. Passwords are saved into the state as plain text and should only be used for testing purposes.",
				Optional:            true,
				Sensitive:           true,
			},
			"suspended": schema.BoolAttribute{
				MarkdownDescription: "Whether the user is suspended.",
				Computed:            true,
				Optional:            true,
				Default:             booldefault.StaticBool(false),
			},
		},
	}
}

func (r *UserResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *UserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data UserResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	me, err := client.User(ctx, codersdk.Me)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
		return
	}
	if len(me.OrganizationIDs) < 1 {
		resp.Diagnostics.AddError("Client Error", "User is not associated with any organizations")
		return
	}

	tflog.Trace(ctx, "creating user")
	loginType := codersdk.LoginType(data.LoginType.ValueString())
	if loginType == codersdk.LoginTypePassword && data.Password.IsNull() {
		resp.Diagnostics.AddError("Data Error", "Password is required when login_type is 'password'")
		return
	}
	if loginType != codersdk.LoginTypePassword && !data.Password.IsNull() {
		resp.Diagnostics.AddError("Data Error", "Password is only allowed when login_type is 'password'")
		return
	}
	user, err := client.CreateUser(ctx, codersdk.CreateUserRequest{
		Email:          data.Email.ValueString(),
		Username:       data.Username.ValueString(),
		Password:       data.Password.ValueString(),
		UserLoginType:  loginType,
		OrganizationID: me.OrganizationIDs[0],
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create user, got error: %s", err))
		return
	}
	tflog.Trace(ctx, "successfully created user", map[string]any{
		"id": user.ID.String(),
	})
	data.ID = UUIDValue(user.ID)

	tflog.Trace(ctx, "updating user profile")
	name := data.Username
	if data.Name.ValueString() != "" {
		name = data.Name
	}
	user, err = client.UpdateUserProfile(ctx, user.ID.String(), codersdk.UpdateUserProfileRequest{
		Username: data.Username.ValueString(),
		Name:     name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update newly created user profile, got error: %s", err))
		return
	}
	tflog.Trace(ctx, "successfully updated user profile")
	data.Name = types.StringValue(user.Name)

	var roles []string
	resp.Diagnostics.Append(
		data.Roles.ElementsAs(ctx, &roles, false)...,
	)
	tflog.Trace(ctx, "updating user roles", map[string]any{
		"new_roles": roles,
	})
	user, err = client.UpdateUserRoles(ctx, user.ID.String(), codersdk.UpdateRoles{
		Roles: roles,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update newly created user roles, got error: %s", err))
		return
	}
	tflog.Trace(ctx, "successfully updated user roles")

	if data.Suspended.ValueBool() {
		_, err = client.UpdateUserStatus(ctx, data.ID.ValueString(), codersdk.UserStatus("suspended"))
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user status, got error: %s", err))
		return
	}
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data UserResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	user, err := client.User(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
		return
	}
	if len(user.OrganizationIDs) < 1 {
		resp.Diagnostics.AddError("Client Error", "User is not associated with any organizations")
		return
	}

	data.Email = types.StringValue(user.Email)
	data.Name = types.StringValue(user.Name)
	data.Username = types.StringValue(user.Username)
	roles := make([]attr.Value, 0, len(user.Roles))
	for _, role := range user.Roles {
		roles = append(roles, types.StringValue(role.Name))
	}
	data.Roles = types.SetValueMust(types.StringType, roles)
	data.LoginType = types.StringValue(string(user.LoginType))
	data.Suspended = types.BoolValue(user.Status == codersdk.UserStatusSuspended)

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data UserResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	user, err := client.User(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
		return
	}
	if len(user.OrganizationIDs) < 1 {
		resp.Diagnostics.AddError("Client Error", "User is not associated with any organizations")
		return
	}

	name := data.Username
	if data.Name.ValueString() != "" {
		name = data.Name
	}
	tflog.Trace(ctx, "updating user", map[string]any{
		"new_username": data.Username.ValueString(),
		"new_name":     name.ValueString(),
	})
	_, err = client.UpdateUserProfile(ctx, user.ID.String(), codersdk.UpdateUserProfileRequest{
		Username: data.Username.ValueString(),
		Name:     name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user profile, got error: %s", err))
		return
	}
	data.Name = name
	tflog.Trace(ctx, "successfully updated user profile")

	var roles []string
	resp.Diagnostics.Append(
		data.Roles.ElementsAs(ctx, &roles, false)...,
	)
	tflog.Trace(ctx, "updating user roles", map[string]any{
		"new_roles": roles,
	})
	_, err = client.UpdateUserRoles(ctx, user.ID.String(), codersdk.UpdateRoles{
		Roles: roles,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user roles, got error: %s", err))
		return
	}
	tflog.Trace(ctx, "successfully updated user roles")

	if data.LoginType.ValueString() == string(codersdk.LoginTypePassword) && !data.Password.IsNull() {
		tflog.Trace(ctx, "updating password")
		err = client.UpdateUserPassword(ctx, user.ID.String(), codersdk.UpdateUserPasswordRequest{
			Password: data.Password.ValueString(),
		})
		if err != nil && !strings.Contains(err.Error(), "New password cannot match old password.") {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update password, got error: %s", err))
			return
		}
		tflog.Trace(ctx, "successfully updated password")
	}

	var statusErr error
	if data.Suspended.ValueBool() {
		_, statusErr = client.UpdateUserStatus(ctx, data.ID.ValueString(), codersdk.UserStatus("suspended"))
	}
	if !data.Suspended.ValueBool() && user.Status == codersdk.UserStatusSuspended {
		_, statusErr = client.UpdateUserStatus(ctx, data.ID.ValueString(), codersdk.UserStatus("active"))
	}
	if statusErr != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user status, got error: %s", err))
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data UserResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	tflog.Trace(ctx, "deleting user")
	err := client.DeleteUser(ctx, data.ID.ValueUUID())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete user, got error: %s", err))
		return
	}
	tflog.Trace(ctx, "successfully deleted user")
}

func (r *UserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
