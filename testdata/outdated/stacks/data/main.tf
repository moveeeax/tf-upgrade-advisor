terraform {
  required_providers {
    aws = {
      source  = "registry.terraform.io/hashicorp/aws"
      version = ">= 6.0"
    }
    template = {
      source  = "hashicorp/template"
      version = "2.2.0"
    }
    corp = {
      source  = "tf.example.com/corp/thing"
      version = "1.0.0"
    }
  }
}

module "pinned_to_yanked" {
  source  = "example/gone/aws"
  version = "1.2.3"
}

module "bucket" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "4.1.0"
}

module "iam" {
  source = "terraform-aws-modules/iam/aws"
}
