# The ID supplied must be the organization UUID and Agents model configuration UUID returned by Coder.
$ terraform import coderd_agents_model.sonnet <organization-id>/<model-config-id>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_agents_model.sonnet
  id = "<organization-id>/<model-config-id>"
}
