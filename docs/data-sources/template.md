---
# generated by https://github.com/hashicorp/terraform-plugin-docs
page_title: "coderd_template Data Source - coderd"
subcategory: ""
description: |-
  An existing template on the coder deployment
---

# coderd_template (Data Source)

An existing template on the coder deployment



<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `id` (String) The ID of the template to retrieve. This field will be populated if a template name is supplied.
- `name` (String) The name of the template to retrieve. This field will be populated if an ID is supplied.
- `organization_id` (String) ID of the organization the template is associated with.

### Read-Only

- `active_user_count` (Number) Number of active users using the template.
- `active_version_id` (String) ID of the active version of the template.
- `activity_bump_ms` (Number) Duration to bump the deadline of a workspace when it receives activity.
- `allow_user_autostart` (Boolean) Whether users can autostart workspaces created from the template.
- `allow_user_autostop` (Boolean) Whether users can customize autostop behavior for workspaces created from the template.
- `allow_user_cancel_workspace_jobs` (Boolean) Whether users can cancel jobs in workspaces created from the template.
- `created_at` (Number) Unix timestamp of when the template was created.
- `created_by_user_id` (String) ID of the user who created the template.
- `default_ttl_ms` (Number) Default time-to-live for workspaces created from the template.
- `deprecated` (Boolean) Whether the template is deprecated.
- `deprecation_message` (String) Message to display when the template is deprecated.
- `description` (String) Description of the template.
- `display_name` (String) Display name of the template.
- `failure_ttl_ms` (Number) Automatic cleanup TTL for failed workspace builds.
- `icon` (String) URL of the template's icon.
- `require_active_version` (Boolean) Whether workspaces created from the template must be up-to-datae on the latest active version.
- `time_til_dormant_autodelete_ms` (Number) Duration of inactivity after the workspace becomes dormant before a workspace is automatically deleted.
- `time_til_dormant_ms` (Number) Duration of inactivity before a workspace is considered dormant.
- `updated_at` (Number) Unix timestamp of when the template was last updated.