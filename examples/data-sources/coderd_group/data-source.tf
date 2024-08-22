// Get a group on the provider default organization by `id`
data "coderd_group" "employees" {
  id = "abcd-efg-hijk"
}

// Get a group on the provider default organization by `name` + `organization_id`
data "coderd_group" "bosses" {
  name = "bosses"
}

// Use them to apply ACL to a template
resource "coderd_template" "example" {
  name     = "example-template"
  versions = [/* ... */]
  acl = {
    groups = [
      {
        id   = data.coderd_group.employees.id
        role = "use"
      },
      {
        id   = data.coderd_group.bosses.id
        role = "admin"
      }
    ]
    users = []
  }
}
