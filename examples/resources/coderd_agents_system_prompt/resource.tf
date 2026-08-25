// Keep the prompt itself in a Markdown file next to the Terraform
// configuration so it can be reviewed like any other prose.
resource "coderd_agents_system_prompt" "this" {
  system_prompt = file("${path.module}/system-prompt.md")

  // Append to Coder's built-in system prompt (the default) rather
  // than replacing it.
  include_default_system_prompt = true
}
