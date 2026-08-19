# The ID must contain the organization UUID and MCP server configuration UUID.
$ terraform import coderd_mcp_server.example <organization-id>/<mcp-server-id>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_mcp_server.example
  id = "<organization-id>/<mcp-server-id>"
}
