# The ID supplied can be either a user UUID retrieved via the API
# or a username.
$ terraform import coderd_ai_user_budget_override.example alice
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_ai_user_budget_override.example
  id = "alice"
}
