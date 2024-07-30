variable "name" {
  type = string
}

resource "local_file" "a" {
  filename = "${path.module}/a.txt"
  content  = "hello ${var.name}"
}

output "a" {
  value = local_file.a.content
}