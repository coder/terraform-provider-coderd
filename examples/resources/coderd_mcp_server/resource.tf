variable "mcp_api_key" {
  type      = string
  sensitive = true
}

resource "coderd_mcp_server" "example" {
  display_name = "Internal Search"
  slug         = "internal-search"
  url          = "https://mcp.example.com/v1"

  auth_type                = "api_key"
  api_key_header           = "Authorization"
  api_key_value_wo         = var.mcp_api_key
  api_key_value_wo_version = 1

  availability    = "default_on"
  tool_allow_list = ["search", "read_document"]
  enabled         = true
  model_intent    = true
}
