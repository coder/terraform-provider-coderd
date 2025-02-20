resource "coderd_organization" "blueberry" {
  name         = "blueberry"
  display_name = "Blueberry"
  description  = "The organization for blueberries"
  icon         = "/emojis/1fad0.png"

  sync_mapping = [
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
