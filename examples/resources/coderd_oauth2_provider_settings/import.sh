# The singleton has no ID; any placeholder works.
# Import before the first apply to adopt the live value.
$ terraform import coderd_oauth2_provider_settings.dcr oauth2_provider_settings
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_oauth2_provider_settings.dcr
  id = "oauth2_provider_settings"
}
