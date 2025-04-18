---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coderd_user Data Source - terraform-provider-coderd"
subcategory: ""
description: |-
  An existing user on the Coder deployment
---

# coderd_user (Data Source)

An existing user on the Coder deployment

## Example Usage

```terraform
// Get a user on the Coder deployment by `id`
data "coderd_user" "manager" {
  id = "abcd-efg-hijk"
}

// Get a user on the Coder deployment by `username`
data "coderd_user" "admin" {
  username = "admin"
}


// Use them to create a group
resource "coderd_group" "bosses" {
  name = "group"
  members = [
    data.coderd_user.admin.id,
    data.coderd_user.manager.id
  ]
}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `id` (String) The ID of the user to retrieve. This field will be populated if a username is supplied.
- `username` (String) The username of the user to retrieve. This field will be populated if an ID is supplied.

### Read-Only

- `avatar_url` (String) URL of the user's avatar.
- `created_at` (Number) Unix timestamp of when the user was created.
- `email` (String) Email of the user.
- `last_seen_at` (Number) Unix timestamp of when the user was last seen.
- `login_type` (String) Type of login for the user. Valid types are `none`, `password', `github`, and `oidc`.
- `name` (String) Display name of the user.
- `organization_ids` (Set of String) IDs of organizations the user is associated with.
- `roles` (Set of String) Roles assigned to the user. Valid roles are `owner`, `template-admin`, `user-admin`, and `auditor`.
- `suspended` (Boolean) Whether the user is suspended.
