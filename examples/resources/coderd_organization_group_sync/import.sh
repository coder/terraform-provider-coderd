# The ID supplied must be an organization UUID
$ terraform import coderd_organization_group_sync.main_group_sync <org-id>
```
Alternatively, in Terraform v1.5.0 and later, an [`import` block](https://developer.hashicorp.com/terraform/language/import) can be used:

```terraform
import {
  to = coderd_organization_group_sync.main_group_sync
  id = "<org-id>"
}
