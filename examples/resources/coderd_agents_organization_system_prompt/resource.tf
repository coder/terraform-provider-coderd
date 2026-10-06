resource "coderd_organization" "engineering" {
  name = "engineering"
}

// Keep the instructions in a Markdown file next to the Terraform
// configuration so they can be reviewed like any other prose.
resource "coderd_agents_organization_system_prompt" "engineering" {
  organization_id = coderd_organization.engineering.id
  system_prompt   = file("${path.module}/engineering-instructions.md")
}
