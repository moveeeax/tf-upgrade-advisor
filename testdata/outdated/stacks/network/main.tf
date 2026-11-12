terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.40"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.8.1"
}

# A subdirectory of a registry package shares the package's versions.
module "http_sg" {
  source  = "terraform-aws-modules/security-group/aws//modules/http-80"
  version = "~> 5.1.0"
}

module "tags" {
  source = "../../modules/tags"
}

module "legacy" {
  source = "git::https://example.com/modules/legacy.git?ref=v1.0.0"
}
