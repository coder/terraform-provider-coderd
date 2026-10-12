resource "coderd_group" "engineering" {
  name = "engineering"
}

resource "coderd_ai_group_budget" "engineering" {
  group_id    = coderd_group.engineering.id
  spend_limit = 50000000 // $50.00 for each member of the group
}
