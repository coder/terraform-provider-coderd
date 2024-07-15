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
  username = "${data.coderd_user.ethan.username}2"
  name = "${data.coderd_user.ethan.name}2"
  email = "${data.coderd_user.ethan.email}.au"
  roles = data.coderd_user.ethan.roles
  suspended = data.coderd_user.ethan.suspended
}

