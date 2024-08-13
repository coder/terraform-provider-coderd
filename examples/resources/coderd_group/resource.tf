// Provider populated from environment variables
provider "coderd" {}

resource "coderd_user" "coder1" {
  username = "coder1"
  name     = "Coder One"
  email    = "coder1@coder.com"
}

resource "coderd_user" "coder2" {
  username = "coder2"
  name     = "Coder One"
  email    = "coder2@coder.com"
}

// Add two users to the group by their ID.
resource "coderd_group" "group1" {
  name = "group1"
  members = [
    coderd_user.coder1.id,
    coderd_user.coder2.id
  ]
}
