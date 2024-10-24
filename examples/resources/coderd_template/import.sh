# The ID supplied can be either a template UUID retrieved via the API
# or a fully qualified name: `<organization-name>/<template-name>`.
$ terraform import coderd_template.example coder/dogfood
```
Once imported, you'll need to manually declare in your config:
- The `versions` list, in order to specify the source directories for new versions of the template.
- (Enterprise) The `acl` attribute, in order to specify the users and groups that have access to the template.

Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_template.example
  id = "coder/dogfood"
}
