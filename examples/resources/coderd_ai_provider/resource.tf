resource "coderd_ai_provider" "bedrock" {
  type         = "bedrock"
  name         = "aws-bedrock"
  display_name = "AWS Bedrock"
  enabled      = true
  base_url     = "https://bedrock-runtime.us-east-1.amazonaws.com"

  settings = {
    bedrock = {
      model            = "anthropic.claude-3-5-sonnet-20241022-v2:0"
      small_fast_model = "anthropic.claude-3-5-haiku-20241022-v1:0"

      // Optional: assume an IAM role via STS before calling Bedrock. Point each
      // provider at a role in the target account for cross-account Bedrock billing.
      // role_arn = "arn:aws:iam::123456789012:role/bedrock-access"

      // Omit these to use the AWS SDK default credential chain from the Coder server
      // process (for example IAM role / IRSA). Set both to use static credentials.
      // access_key_wo          = var.bedrock_access_key
      // access_key_secret_wo   = var.bedrock_access_key_secret
      // credentials_wo_version = 1
    }
  }
}

resource "coderd_ai_provider" "openai" {
  type         = "openai"
  name         = "openai"
  display_name = "OpenAI"
  enabled      = true
  base_url     = "https://api.openai.com/v1"

  api_key_wo         = var.openai_api_key
  api_key_wo_version = 1
}
