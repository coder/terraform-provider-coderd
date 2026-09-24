resource "coderd_ai_model_price" "opus" {
  provider_type = "anthropic"
  model         = "claude-opus-4-1"
  input_price   = 15000000 // $15.00 per 1M input tokens
  output_price  = 75000000 // $75.00 per 1M output tokens
}

resource "coderd_ai_model_price" "sonnet" {
  provider_type     = "anthropic"
  model             = "claude-sonnet-4-5"
  input_price       = 3000000  // $3.00 per 1M input tokens
  output_price      = 15000000 // $15.00 per 1M output tokens
  cache_read_price  = 300000   // $0.30 per 1M cached input tokens
  cache_write_price = 3750000  // $3.75 per 1M tokens written to the cache
}
