terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_organization" "test" {
  name         = "test-org-group-sync"
  display_name = "Test Organization for Group Sync"
  description  = "Organization created for testing group sync functionality"
}

resource "coderd_group" "test" {
  organization_id = coderd_organization.test.id
  name            = "test-group"
  display_name    = "Test Group"
  quota_allowance = 50
}

resource "coderd_group" "admins" {
  organization_id = coderd_organization.test.id
  name            = "admin-group"
  display_name    = "Admin Group"
  quota_allowance = 100
}

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

data "coderd_organization" "test_data" {
  id = coderd_organization.test.id
}
