resource "coderd_organization_group_sync" "test" {
  organization_id     = coderd_organization.test.id
  field               = "groups"
  regex_filter        = "test_.*|admin_.*"
  auto_create_missing = false

  mapping = {
    "test_developers" = [coderd_group.test.id]
    "admin_users"     = [coderd_group.admins.id]
    "mixed_group"     = [coderd_group.test.id, coderd_group.admins.id]
  }
}
