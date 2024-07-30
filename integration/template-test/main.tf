terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_user" "ethan" {
  username   = "ethan"
  name       = "Ethan Coolguy"
  email      = "test@coder.com"
  roles      = ["owner", "template-admin"]
  login_type = "password"
  password   = "SomeSecurePassword!"
  suspended  = false
}

data "coderd_organization" "default" {
  is_default = true
}

resource "coderd_template" "sample" {
  name                  = "example-template"
  allow_user_auto_stop  = false
  allow_user_auto_start = false
  acl = {
    groups = [
      {
        id   = data.coderd_organization.default.id
        role = "use"
      }
    ]
    users = [
      {
        id   = resource.coderd_user.ethan.id
        role = "admin"
      }
    ]
  }
  versions = [
    {
      name_prefix = "latest"
      directory   = "./version-one"
      active      = true
      tf_vars = [
        {
          name  = "name"
          value = "world"
        },
      ]
    },
    {
      name_prefix = "legacy"
      directory   = "./version-two"
      active      = false
      tf_vars = [
        {
          name  = "name"
          value = "ethan"
        },
      ]
    }
  ]
}

data "coderd_template" "sample" {
  organization_id = data.coderd_organization.default.id
  name            = coderd_template.sample.name
}
