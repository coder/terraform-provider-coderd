terraform {
  required_providers {
    coderd = {
      source = "coder/coderd"
    }
  }
}

provider "coderd" {
  url   = "coder.example.com"
  token = "****"
}


data "coderd_organization" "default" {
  is_default = true
}

data "coderd_user" "admin" {
  username = "admin"
}

resource "coderd_user" "manager" {
  username = "Manager"
  email    = "manager@example.com"
}

resource "coderd_group" "bosses" {
  name = "group"
  members = [
    data.coderd_user.admin.id,
    resource.coderd_user.manager.id
  ]
}

resource "coderd_template" "example" {
  name = "example-template"
  versions = [{
    directory = "./example-template"
    active    = true
    tf_vars = [{
      name  = "image_id"
      value = "ami-12345678"
    }]
    # Version names can be randomly generated if null/omitted
  }]
  acl = {
    groups = [{
      id   = data.coderd_organization.default.id
      role = "use"
      },
      {
        id   = resource.coderd_group.bosses.id
        role = "admin"
    }]
    users = []
  }
  allow_user_cancel_workspace_jobs = false
}
