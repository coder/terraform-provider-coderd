// Provider populated from environemnt variables
provider "coderd" {}

// Create a bot user for Jenkins
resource "coderd_user" "jenkins" {
  username   = "jenkins"
  name       = "Jenkins CI/CD"
  email      = "ci@example.com"
  roles      = ["template-admin"]
  login_type = "none"
}

// Keep the password of a user account up to date from an external source
resource "coderd_user" "audit" {
  username   = "auditor"
  name       = "Auditor"
  email      = "security@example.com"
  roles      = ["auditor"]
  login_type = "password"
  password   = data.vault_password.auditor.value
}

// Ensure the admin account is suspended
resource "coderd_user" "admin" {
  username  = "admin"
  suspended = true
  email     = "admin@example.com"
}

// Create a service account for automation (Premium). Unlike a `login_type =
// none` user, a service account has no email and does not consume a user seat.
resource "coderd_user" "automation" {
  username           = "automation"
  name               = "Automation"
  roles              = ["template-admin"]
  is_service_account = true
}
