---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coderd_user Resource - terraform-provider-coderd"
subcategory: ""
description: |-
  A user on the Coder deployment.
  When importing, the ID supplied can be either a user UUID or a username.
---

# coderd_user (Resource)

A user on the Coder deployment.

When importing, the ID supplied can be either a user UUID or a username.

## Example Usage

```terraform
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `email` (String) Email address of the user.
- `username` (String) Username of the user.

### Optional

- `login_type` (String) Type of login for the user. Valid types are `none`, `password`, `github`, and `oidc`.
- `name` (String) Display name of the user. Defaults to username.
- `password` (String, Sensitive) Password for the user. Required when `login_type` is `password`. Passwords are saved into the state as plain text and should only be used for testing purposes.
- `roles` (Set of String) Roles assigned to the user. Valid roles are `owner`, `template-admin`, `user-admin`, and `auditor`.
- `suspended` (Boolean) Whether the user is suspended.

### Read-Only

- `id` (String) User ID
