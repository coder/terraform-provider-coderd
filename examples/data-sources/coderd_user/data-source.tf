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
