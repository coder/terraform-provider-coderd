terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

resource "coderd_ai_provider" "anthropic" {
  type         = "anthropic"
  name         = "agents-anthropic"
  display_name = "Anthropic"
  base_url     = "https://api.anthropic.com/"

  api_key_wo         = "sk-ant-api03-integration-test"
  api_key_wo_version = 1
}

resource "coderd_ai_provider" "openai" {
  type         = "openai"
  name         = "agents-openai"
  display_name = "OpenAI"
  base_url     = "https://api.openai.com/v1"

  api_key_wo         = "sk-integration-test-openai"
  api_key_wo_version = 1
}

resource "coderd_agents_model" "claude_opus" {
  ai_provider_id        = coderd_ai_provider.anthropic.id
  model                 = "claude-opus-4-8"
  display_name          = "Claude Opus 4.8"
  context_limit         = 1000000
  compression_threshold = 42

  model_config = jsonencode({
    max_output_tokens = 128000
    cost = {
      input_price_per_million_tokens       = "5"
      output_price_per_million_tokens      = "25"
      cache_read_price_per_million_tokens  = "0.5"
      cache_write_price_per_million_tokens = "6.25"
    }
    provider_options = {
      anthropic = {
        send_reasoning = true
        effort         = "high"
      }
    }
  })
}

resource "coderd_agents_model" "claude_sonnet" {
  ai_provider_id        = coderd_ai_provider.anthropic.id
  model                 = "claude-sonnet-4-6"
  display_name          = "Claude Sonnet 4.6"
  context_limit         = 200000
  compression_threshold = 70

  model_config = jsonencode({
    cost = {
      input_price_per_million_tokens  = "3"
      output_price_per_million_tokens = "15"
    }
    provider_options = {
      anthropic = {
        send_reasoning     = true
        effort             = "max"
        web_search_enabled = true
        thinking = {
          budget_tokens = 16000
        }
      }
    }
  })
}

resource "coderd_agents_model" "gpt_xhigh" {
  ai_provider_id        = coderd_ai_provider.openai.id
  model                 = "gpt-5.5"
  display_name          = "GPT-5.5"
  context_limit         = 272000
  compression_threshold = 70

  model_config = jsonencode({
    cost = {
      input_price_per_million_tokens      = "2.5"
      output_price_per_million_tokens     = "15"
      cache_read_price_per_million_tokens = "0.25"
    }
    provider_options = {
      openai = {
        parallel_tool_calls = false
        reasoning_effort    = "xhigh"
        reasoning_summary   = "detailed"
        text_verbosity      = "high"
        web_search_enabled  = true
        search_context_size = "medium"
      }
    }
  })
}

resource "coderd_agents_model" "gpt_mini" {
  ai_provider_id        = coderd_ai_provider.openai.id
  model                 = "gpt-5.4-mini"
  display_name          = "GPT-5.4 Mini"
  context_limit         = 400000
  compression_threshold = 70

  model_config = jsonencode({
    provider_options = {
      openai = {
        reasoning_effort = "medium"
      }
    }
  })
}
