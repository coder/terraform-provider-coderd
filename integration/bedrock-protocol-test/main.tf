terraform {
  required_providers {
    coderd = {
      source  = "coder/coderd"
      version = ">=0.0.0"
    }
  }
}

# Exercises the Bedrock protocol passthrough (dogfood PR #434 scenario): a
# single Mantle provider serves multiple Anthropic models without requiring
# model/small_fast_model, which the invoke-model protocol would demand. The
# control plane (provider creation) is exercised here; inference needs real
# AWS credentials and is out of scope for the containerized integration test.
resource "coderd_ai_provider" "bedrock" {
  type         = "bedrock"
  name         = "bedrock-protocol"
  display_name = "AWS Bedrock (mantle)"
  enabled      = true
  base_url     = "https://bedrock-mantle.us-east-1.api.aws/anthropic"

  settings = {
    bedrock = {
      region   = "us-east-1"
      protocol = "mantle"
    }
  }
}
