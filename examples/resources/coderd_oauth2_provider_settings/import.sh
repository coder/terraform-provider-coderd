# This setting is a deployment-wide singleton with no identifying attribute, so
# the import ID is an unused placeholder. Any string works; a fixed one keeps
# the command copy-pasteable.
#
# Import before your first apply when adopting a deployment that already has
# this setting configured: state is then populated from the live value, and the
# following `terraform plan` shows an honest diff instead of silently
# overwriting it.
$ terraform import coderd_oauth2_provider_settings.dcr oauth2_provider_settings
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_oauth2_provider_settings.dcr
  id = "oauth2_provider_settings"
}
