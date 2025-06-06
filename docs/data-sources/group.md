---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coderd_group Data Source - terraform-provider-coderd"
subcategory: ""
description: |-
  An existing group on the Coder deployment.
---

# coderd_group (Data Source)

An existing group on the Coder deployment.

## Example Usage

```terraform
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `id` (String) The ID of the group to retrieve. This field will be populated if a name and organization ID is supplied.
- `name` (String) The name of the group to retrieve. This field will be populated if an ID is supplied.
- `organization_id` (String) The organization ID that the group belongs to. This field will be populated if an ID is supplied. Defaults to the provider default organization ID.

### Read-Only

- `avatar_url` (String)
- `display_name` (String)
- `members` (Attributes Set) Members of the group. (see [below for nested schema](#nestedatt--members))
- `quota_allowance` (Number) The number of quota credits to allocate to each user in the group.
- `source` (String) The source of the group. Either `oidc` or `user`.

<a id="nestedatt--members"></a>
### Nested Schema for `members`

Read-Only:

- `created_at` (Number) Unix timestamp of when the member was created.
- `email` (String)
- `id` (String)
- `last_seen_at` (Number) Unix timestamp of when the member was last seen.
- `login_type` (String) The login type of the member. Can be `oidc`, `token`, `password`, `github` or `none`.
- `status` (String) The status of the member. Can be `active`, `dormant` or `suspended`.
- `username` (String)
