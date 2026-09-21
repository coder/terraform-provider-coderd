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

variable "openai_api_key" {
  type      = string
  sensitive = true
}

resource "coderd_ai_provider" "openai" {
  type     = "openai"
  name     = "openai"
  base_url = "https://api.openai.com/v1"

  api_key_wo         = var.openai_api_key
  api_key_wo_version = 1
}

# reasoning_mode = "pro" needs a Coder release newer than 2.37 and an OpenAI
# model from the GPT-5.6 Sol generation onward; OpenAI rejects requests for
# models that do not support it.
resource "coderd_agents_model" "gpt_6_astra_pro" {
  ai_provider_id = coderd_ai_provider.openai.id
  model          = "gpt-6-astra"
  display_name   = "GPT-6 Astra (Pro)"
  enabled        = true
  context_limit  = 400000

  model_config = jsonencode({
    reasoning_effort = {
      default = "high"
      max     = "xhigh"
    }
    provider_options = {
      openai = {
        reasoning_mode = "pro"
      }
    }
  })
}
