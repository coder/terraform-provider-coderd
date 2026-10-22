resource "coderd_user" "alice" {
  username   = "alice"
  email      = "alice@example.com"
  login_type = "password"
  password   = "SomeSecurePassword!"
}

resource "coderd_group" "engineering" {
  name    = "engineering"
  members = [coderd_user.alice.id]
}

resource "coderd_ai_group_budget" "engineering" {
  group_id    = coderd_group.engineering.id
  spend_limit = 50000000 // $50.00 for each member of the group
}

resource "coderd_ai_user_budget_override" "alice" {
  user_id     = coderd_user.alice.id
  group_id    = coderd_group.engineering.id
  spend_limit = 200000000 // $200.00 for alice, attributed to engineering
}
