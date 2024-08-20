// Get a template on the provider's default organization by `id`
data "coderd_template" "ubuntu-main" {
  id = "abcd-efg-hijk"
}

// Get a template on the provider's default organization by `name`
data "coderd_template" "windows-main" {
  name = "windows-main"
}

// Manage a template resource with the same permissions & settings as an existing template
resource "coderd_template" "debian-main" {
  name                              = "debian-main"
  versions                          = [/* ... */]
  acl                               = data.coderd_template.ubuntu-main.acl
  allow_user_auto_start             = data.coderd_template.ubuntu-main.allow_user_auto_start
  auto_start_permitted_days_of_week = data.coderd_template.ubuntu-main.auto_start_permitted_days_of_week
  allow_user_auto_stop              = data.coderd_template.ubuntu-main.allow_user_auto_stop
  auto_stop_requirement             = data.coderd_template.ubuntu-main.auto_stop_requirement
  allow_user_cancel_workspace_jobs  = data.coderd_template.ubuntu-main.allow_user_cancel_workspace_jobs
}
