# The ID supplied is a coderd_agents_model UUID, e.g. coderd_agents_model.<name>.id.
$ terraform import coderd_default_agents_model.default <model-config-id>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_default_agents_model.default
  id = "<model-config-id>"
}
