# Import by the Agents model configuration UUID returned by Coder.
$ terraform import coderd_agents_model.sonnet 00000000-0000-0000-0000-000000000000
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_agents_model.sonnet
  id = "00000000-0000-0000-0000-000000000000"
}
