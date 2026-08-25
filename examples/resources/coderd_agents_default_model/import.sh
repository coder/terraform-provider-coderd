# The ID supplied is the organization UUID whose default model should be imported.
$ terraform import coderd_agents_default_model.default <organization-id>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_agents_default_model.default
  id = "<organization-id>"
}
