package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var agentsModelACLAttrTypes = map[string]attr.Type{
	"users":  types.SetType{ElemType: UUIDType},
	"groups": types.SetType{ElemType: UUIDType},
}

type agentsModelACL struct {
	Users  []UUID `tfsdk:"users"`
	Groups []UUID `tfsdk:"groups"`
}

func agentsModelACLAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "Users and groups that can use this model. Owners and organization admins can use every model. " +
			"Coder gives the organization's `Everyone` group access to new models.",
		Optional: true,
		Computed: true,
		PlanModifiers: []planmodifier.Object{
			objectplanmodifier.UseStateForUnknown(),
		},
		Attributes: map[string]schema.Attribute{
			// Computed with IsRequired, not Required: if a child is not
			// computed, Terraform core plans an omitted acl as null, so every
			// plan marks updated_at unknown. No Default either: the framework
			// applies nested defaults when acl is omitted, which would clear
			// the list.
			"users": schema.SetAttribute{
				MarkdownDescription: "IDs of users that can use the model. Required if `acl` is set. Each user must be a member of the model's organization.",
				ElementType:         UUIDType,
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Set{setvalidator.IsRequired()},
			},
			"groups": schema.SetAttribute{
				MarkdownDescription: "IDs of groups that can use the model. Required if `acl` is set. Each group must belong to the model's organization. " +
					"To keep access for the `Everyone` group, include the organization ID.",
				ElementType: UUIDType,
				Optional:    true,
				Computed:    true,
				Validators:  []validator.Set{setvalidator.IsRequired()},
			},
		},
	}
}

func agentsModelACLValue(acl codersdk.ChatModelACL) types.Object {
	users := make([]attr.Value, 0, len(acl.Users))
	for _, user := range acl.Users {
		users = append(users, UUIDValue(user.ID))
	}
	groups := make([]attr.Value, 0, len(acl.Groups))
	for _, group := range acl.Groups {
		groups = append(groups, UUIDValue(group.ID))
	}
	return types.ObjectValueMust(agentsModelACLAttrTypes, map[string]attr.Value{
		"users":  types.SetValueMust(UUIDType, users),
		"groups": types.SetValueMust(UUIDType, groups),
	})
}

func agentsModelACLUpdate(current codersdk.ChatModelACL, want agentsModelACL) codersdk.UpdateChatModelACLRequest {
	userIDs := make([]uuid.UUID, 0, len(current.Users))
	for _, user := range current.Users {
		userIDs = append(userIDs, user.ID)
	}
	groupIDs := make([]uuid.UUID, 0, len(current.Groups))
	for _, group := range current.Groups {
		groupIDs = append(groupIDs, group.ID)
	}
	return codersdk.UpdateChatModelACLRequest{
		UserRoles:  agentsModelACLRoles(memberDiff(userIDs, want.Users)),
		GroupRoles: agentsModelACLRoles(memberDiff(groupIDs, want.Groups)),
	}
}

func agentsModelACLRoles(add, remove []string) map[string]codersdk.ChatRole {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	roles := make(map[string]codersdk.ChatRole, len(add)+len(remove))
	for _, id := range add {
		roles[id] = codersdk.ChatRoleRead
	}
	for _, id := range remove {
		roles[id] = codersdk.ChatRoleDeleted
	}
	return roles
}

func (r *AgentsModelResource) readACL(ctx context.Context, organizationID, modelID uuid.UUID, diags *diag.Diagnostics) (codersdk.ChatModelACL, bool) {
	acl, err := r.data.Client.ChatModelACL(ctx, organizationID, modelID)
	if err == nil {
		return acl, true
	}
	detail := fmt.Sprintf("Unable to read Agents model access list, got error: %s", err)
	if isHTTPNotFound(err) {
		detail += "\n\nCoder returns 404 when the token cannot share chat models. Use a token for a user with the Owner or Organization Admin role."
	}
	diags.AddError("Client Error", detail)
	return codersdk.ChatModelACL{}, false
}

// syncACL diffs against a fresh read, not state, so that it also removes
// entries that state does not know about.
func (r *AgentsModelResource) syncACL(ctx context.Context, organizationID, modelID uuid.UUID, planned types.Object, diags *diag.Diagnostics) types.Object {
	current, ok := r.readACL(ctx, organizationID, modelID, diags)
	if !ok {
		return types.ObjectNull(agentsModelACLAttrTypes)
	}
	if planned.IsNull() || planned.IsUnknown() {
		return agentsModelACLValue(current)
	}
	var want agentsModelACL
	diags.Append(planned.As(ctx, &want, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return types.ObjectNull(agentsModelACLAttrTypes)
	}
	req := agentsModelACLUpdate(current, want)
	if req.UserRoles == nil && req.GroupRoles == nil {
		return planned
	}
	if err := r.data.Client.UpdateChatModelACL(ctx, organizationID, modelID, req); err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Unable to update Agents model access list, got error: %s", err))
		return types.ObjectNull(agentsModelACLAttrTypes)
	}
	return planned
}
