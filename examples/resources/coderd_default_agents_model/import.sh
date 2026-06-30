# The ID supplied is the coderd_agents_model UUID to mark as the deployment-wide
# default. Coder reconciles to whichever model it currently reports as default on
# the next read, so a stale value self-corrects.
$ terraform import coderd_default_agents_model.default <model-config-id>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_default_agents_model.default
  id = "<model-config-id>"
}
