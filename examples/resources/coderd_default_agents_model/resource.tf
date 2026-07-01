variable "anthropic_api_key" {
  type      = string
  sensitive = true
}

resource "coderd_ai_provider" "anthropic" {
  type     = "anthropic"
  name     = "anthropic"
  base_url = "https://api.anthropic.com"

  api_key_wo         = var.anthropic_api_key
  api_key_wo_version = 1
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = coderd_ai_provider.anthropic.id
  model          = "claude-3-5-sonnet-20241022"
  display_name   = "Claude 3.5 Sonnet"
  context_limit  = 200000
}

# Mark the Sonnet model as the deployment-wide default for Coder Agents.
# Setting a new default automatically demotes the previous one, so only a single
# coderd_default_agents_model resource should exist per deployment.
resource "coderd_default_agents_model" "default" {
  model_id = coderd_agents_model.sonnet.id
}
