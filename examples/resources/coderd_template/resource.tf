// Provider populated from environment variables
provider "coderd" {}

// Get the commit SHA of the configuration's git repository
variable "TFC_CONFIGURATION_VERSION_GIT_COMMIT_SHA" {
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
      name        = "stable-${var.TFC_CONFIGURATION_VERSION_GIT_COMMIT_SHA}"
      description = "The stable version of the template."
      directory   = "./stable-template"
    },
    {
      name        = "staging-${var.TFC_CONFIGURATION_VERSION_GIT_COMMIT_SHA}"
      description = "The staging version of the template."
      directory   = "./staging-template"
    }
  ]
}
