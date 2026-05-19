terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_template" "port_share" {
  name                 = "port-share-test"
  max_port_share_level = "organization"
  versions = [
    {
      directory = "./example-template"
      active    = true
    }
  ]
}
