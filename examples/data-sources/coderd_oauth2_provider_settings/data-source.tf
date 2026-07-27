// Read the deployment's OAuth2 provider settings without taking ownership of
// them. Use this when another configuration (or nobody at all) owns the
// `coderd_oauth2_provider_settings` resource, since the setting is a
// deployment-wide singleton and only one configuration can manage it.
//
// This only issues a GET, so it also works with tokens that can read but not
// write the deployment configuration, such as an Auditor token.
//
// It does still require the deployment's `oauth2` experiment to be enabled: the
// experiment gates the entire /api/v2/oauth2-provider route, so a read-only
// token does not get you around it.
data "coderd_oauth2_provider_settings" "current" {}

// Because Dynamic Client Registration is disabled, provision a static OAuth2
// client instead, for MCP clients that cannot register themselves.
resource "coderd_group" "mcp_static_clients" {
  count = data.coderd_oauth2_provider_settings.current.dynamic_client_registration_enabled ? 0 : 1

  name            = "mcp-static-clients"
  organization_id = data.coderd_organization.default.id
}

output "dynamic_client_registration_enabled" {
  value = data.coderd_oauth2_provider_settings.current.dynamic_client_registration_enabled
}
