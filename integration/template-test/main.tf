terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

provider "coderd" {
  url   = "http://localhost:3000"
  token = "NbRNSwdzeb-Npwlm9TIOX3bpEQIsgt2KI"
}

resource "coderd_user" "ethan" {
  username   = "dean"
  name       = "Dean Coolguy"
  email      = "deantest@coder.com"
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
      directory = "./example-template-2"
      active    = true
      tf_vars = [
        {
          name  = "name"
          value = "world"
        },
      ]
    },
    {
      directory = "./example-template-2"
      active    = false
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
