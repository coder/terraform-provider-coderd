terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_user" "alice" {
  username = "alice"
  email    = "alice@example.com"
}

resource "coderd_group" "engineering" {
  name    = "engineering"
  members = [coderd_user.alice.id]
}

resource "coderd_ai_group_budget" "engineering" {
  group_id    = coderd_group.engineering.id
  spend_limit = 50000000
}

# The override takes precedence over the group budget.
resource "coderd_ai_user_budget_override" "alice" {
  user_id     = coderd_user.alice.id
  group_id    = coderd_group.engineering.id
  spend_limit = 200000000
}
