# The ID supplied can be either an AI provider UUID or name.
# Existing remote API keys are preserved. Omit api_key_wo and api_key_wo_version
# to leave them unmanaged, or configure both to replace them on a later apply.
$ terraform import coderd_ai_provider.example openai
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_ai_provider.example
  id = "openai"
}
