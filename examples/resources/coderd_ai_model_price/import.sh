# The ID supplied must be <provider_type>/<model>. The model part can contain
# "/", for example openrouter/anthropic/claude-sonnet-4.5.
$ terraform import coderd_ai_model_price.example anthropic/claude-sonnet-4-5
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_ai_model_price.example
  id = "anthropic/claude-sonnet-4-5"
}
