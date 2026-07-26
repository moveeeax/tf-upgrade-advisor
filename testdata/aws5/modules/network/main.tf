resource "aws_ssm_association" "patch" {
  name        = "AWS-RunPatchBaseline"
  instance_id = "i-00000000000000000"
}

resource "aws_redshift_cluster" "analytics" {
  cluster_identifier = "analytics"
  node_type          = "ra3.xlplus"
  database_name      = "analytics"
  master_username    = "admin"
}

data "aws_ami" "amazon_linux" {
  most_recent = true

  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }
}
