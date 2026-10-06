# The ID supplied must be the organization name or UUID.
$ terraform import coderd_agents_organization_system_prompt.engineering <organization-name>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_agents_organization_system_prompt.engineering
  id = "<organization-name>"
}
