# The ID supplied can be either a organization UUID retrieved via the API
# or the name of the organization.
$ terraform import coderd_organization.our_org our-org
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_organization.our_org
  id = "our-org"
}
