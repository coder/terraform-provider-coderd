resource "coderd_license" "license" {
  license = "<…>"

  lifecycle {
    create_before_destroy = true
  }
}
