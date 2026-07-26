terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = "eu-central-1"
}

resource "aws_eip" "nat" {
  domain = "vpc"
}

resource "aws_flow_log" "vpc" {
  log_destination = "arn:aws:logs:eu-central-1:123456789012:log-group:vpc-flow-logs"
  iam_role_arn    = "arn:aws:iam::123456789012:role/example"
  vpc_id          = "vpc-00000000000000000"
  traffic_type    = "ALL"
}

resource "aws_s3_bucket_versioning" "state" {
  bucket = "example-state"

  versioning_configuration {
    status = "Enabled"
  }
}
