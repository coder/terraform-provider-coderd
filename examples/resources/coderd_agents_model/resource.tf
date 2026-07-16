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
  enabled        = true
  context_limit  = 200000

  model_config = jsonencode({
    max_output_tokens = 8192
    temperature       = 0.7
    cost = {
      input_price_per_million_tokens  = "3"
      output_price_per_million_tokens = "15"
    }
    reasoning_effort = {
      default = "high"
      max     = "high"
    }
    provider_options = {
      anthropic = {
        thinking = { budget_tokens = 4096 }
      }
    }
  })
}
