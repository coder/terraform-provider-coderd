terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_user" "dean" {
  username   = "dean"
  name       = "Dean Coolguy"
  email      = "test@coder.com"
  roles      = ["owner", "template-admin"]
  login_type = "password"
  password   = "SomeSecurePassword!"
  suspended  = false
}

data "coderd_user" "ethan" {
  username = "ethan"
}

resource "coderd_user" "ethan2" {
  username  = "${data.coderd_user.ethan.username}2"
  name      = "${data.coderd_user.ethan.name}2"
  email     = "${data.coderd_user.ethan.email}.au"
  roles     = data.coderd_user.ethan.roles
  suspended = data.coderd_user.ethan.suspended
}

data "coderd_organization" "default" {
  is_default = true
}

data "coderd_group" "bosses" {
  name            = "bosses"
  organization_id = data.coderd_organization.default.id
}

resource "coderd_group" "employees" {
  name            = "employees"
  organization_id = data.coderd_organization.default.id
  quota_allowance = 100
  members = concat([
    resource.coderd_user.dean.id,
    resource.coderd_user.ethan2.id,
  ], data.coderd_group.bosses.members[*].id)
}

data "coderd_group" "employees" {
  id = resource.coderd_group.employees.id
}
