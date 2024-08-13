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
}
