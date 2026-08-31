# The ID must contain the organization name and the MCP server slug.
$ terraform import coderd_agents_mcp_server.example <organization-name>/<slug>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_agents_mcp_server.example
  id = "<organization-name>/<slug>"
}
