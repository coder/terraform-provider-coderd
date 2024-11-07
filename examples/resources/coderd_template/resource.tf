// Provider populated from environment variables
provider "coderd" {}

// Can be populated using an environment variable, or an external datasource script
variable "COMMIT_SHA" {
  type = string
}

resource "coderd_user" "coder1" {
  username = "coder1"
  name     = "Coder One"
  email    = "coder1@coder.com"
}

resource "coderd_template" "ubuntu-main" {
  name        = "ubuntu-main"
  description = "The main template for developing on Ubuntu."
  versions = [
    {
      name        = "stable-${var.COMMIT_SHA}"
      description = "The stable version of the template."
      directory   = "./stable-template"
    },
    {
      name        = "staging-${var.COMMIT_SHA}"
      description = "The staging version of the template."
      directory   = "./staging-template"
    }
  ]
  acl = {
    users = [{
      id   = coderd_user.coder1.id
      role = "admin"
    }]
    groups = []
  }
}
