// Provider populated from environment variables
provider "coderd" {}

// Can be populated using an environment variable, or an external datasource script
variable "COMMIT_SHA" {
  type = string
}

resource "coderd_user" "coder1" {
  username = "coder1"
  name     = "Coder One"
  email    = "coder1@coder.com"
}

resource "coderd_template" "ubuntu-main" {
  name        = "ubuntu-main"
  description = "The main template for developing on Ubuntu."
  versions = [
    {
      name        = "stable-${var.COMMIT_SHA}"
      description = "The stable version of the template."
      directory   = "./stable-template"
    },
    {
      name        = "staging-${var.COMMIT_SHA}"
      description = "The staging version of the template."
      directory   = "./staging-template"
    }
  ]
  acl = {
    users = [{
      id   = coderd_user.coder1.id
      role = "admin"
    }]
    groups = []
  }
}

// Template contents don't have to live in a directory on disk: a version can be
// built from rendered file contents, or from a prebuilt archive. Exactly one of
// `directory`, `files`, `archive_base64`, and `archive_file` may be set on a
// version.
resource "coderd_template" "ubuntu-rendered" {
  name        = "ubuntu-rendered"
  description = "The main template, rendered for this deployment."
  versions = [
    {
      name   = "rendered-${var.COMMIT_SHA}"
      active = true
      files = {
        "main.tf"          = templatefile("${path.module}/tpl/main.tf.tftpl", { image = "ubuntu:24.04" })
        "build/Dockerfile" = file("${path.module}/tpl/build/Dockerfile")
      }
    },
    {
      name = "prebuilt-${var.COMMIT_SHA}"
      // A base64-encoded tar, tar.gz, or zip archive, such as one fetched from
      // an artifact store. Variable values aren't discovered from an archive, so
      // they're set with `tf_vars`.
      archive_base64 = filebase64("${path.module}/tpl/prebuilt.tar")
      tf_vars = [{
        name  = "image"
        value = "ubuntu:24.04"
      }]
    },
    {
      name = "packaged-${var.COMMIT_SHA}"
      // The same archive, referenced by path instead of being read into the
      // configuration. The file has to exist at plan time, so a data source is
      // a better fit than a resource that writes it during apply.
      archive_file = data.archive_file.template.output_path
      tf_vars = [{
        name  = "image"
        value = "ubuntu:24.04"
      }]
    }
  ]
}

data "archive_file" "template" {
  type        = "zip"
  source_dir  = "${path.module}/stable-template"
  output_path = "${path.module}/build/template.zip"
}
