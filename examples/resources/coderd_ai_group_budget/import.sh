# The ID supplied can be either a group UUID retrieved via the API
# or a fully qualified name: `<organization-name>/<group-name>`.
$ terraform import coderd_ai_group_budget.example coder/engineering
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_ai_group_budget.example
  id = "coder/engineering"
}
