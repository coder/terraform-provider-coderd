// Get the default (first) organization for the coder deployment
data "coderd_organization" "default" {
  is_default = true
}

// Get another organization by `id`
data "coderd_organization" "example" {
  id = "abcd-efg-hijk"
}

// Or get by name
data "coderd_organization" "example2" {
  name = "example-organization-2"
}

// Create a group on a specific organization
resource "coderd_group" "example" {
  name            = "example-group"
  organization_id = data.coderd_organization.example.id
}
