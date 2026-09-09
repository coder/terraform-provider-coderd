resource "coderd_organization" "blueberry" {
  name         = "blueberry"
  display_name = "Blueberry"
  description  = "The organization for blueberries"
  icon         = "/emojis/1fad0.png"

  # Requires the `minimum-implicit-member` experiment when set to
  # anything other than the deployment default.
  default_org_member_roles = [
    "organization-workspace-access",
  ]

  org_sync_idp_groups = [
    "wibble",
    "wobble",
  ]

  group_sync {
    field = "coder_groups"
    mapping = {
      toast = [coderd_group.bread.id]
    }
  }

  role_sync {
    field = "coder_roles"
    mapping = {
      manager = ["organization-user-admin"]
    }
  }
}
