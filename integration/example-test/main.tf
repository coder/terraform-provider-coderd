terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_example" "example" {
  configurable_attribute = "some-value"
}
