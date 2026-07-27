// Important note: this setting is a deployment-wide singleton, so you can only
// have one resource of this type! Declaring it twice is not an error, the
// blocks will silently overwrite each other on every apply.
//
// If the deployment already has this setting configured (via the CLI or the
// deployment settings UI), run `terraform import` before your first apply, or
// this resource will overwrite the live value without showing a diff.
resource "coderd_oauth2_provider_settings" "dcr" {
  dynamic_client_registration_enabled = true

  // Only needed when the same configuration also upgrades Coder itself.
  // `/api/v2/oauth2-provider/settings` does not exist until the new version is
  // actually serving, and `depends_on = [helm_release.coder]` alone only orders
  // "the Helm apply returned", which for a rolling update can still leave
  // old-version pods behind the load balancer. Gate on the API responding
  // instead, or apply the upgrade and this resource in two separate runs.
  depends_on = [terraform_data.coder_ready]
}

resource "terraform_data" "coder_ready" {
  depends_on = [helm_release.coder]

  provisioner "local-exec" {
    command = "until curl -sfo /dev/null $CODER_URL/api/v2/buildinfo; do sleep 5; done"
  }
}
