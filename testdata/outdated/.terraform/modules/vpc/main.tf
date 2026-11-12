# Downloaded by `terraform init`; the inventory must not count it.
module "nested" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "1.0.0"
}
