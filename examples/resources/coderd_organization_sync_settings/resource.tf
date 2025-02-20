// Important note: You can only have one resource of this type!
resource "coderd_organization_sync_settings" "org_sync" {
  field          = "wibble"
  assign_default = false

  mapping = {
    wobble = [
      coderd_organization.my_organization.id,
    ]
  }
}
