# The ID supplied can be either a template UUID retrieved via the API
# or a fully qualified name: `<organization-name>/<template-name>`.
$ terraform import coderd_template.example coder/dogfood
```
Once imported, you'll need to manually declare in your config:
- (Enterprise) The `acl` attribute, in order to specify the users and groups that have access to the template.
- The `versions` list, if you want Terraform to manage the template's versions. If
  you omit `versions` (or leave it `null`), Terraform will only manage the
  template's other settings (ACL, dormancy, TTLs, etc.) and will not create,
  update, or read any template versions — this is useful when versions are pushed
  by an external pipeline (e.g. via `coder templates push`).

Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_template.example
  id = "coder/dogfood"
}
