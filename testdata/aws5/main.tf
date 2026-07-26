terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.40"
    }
  }
}

provider "aws" {
  region = "eu-central-1"

  endpoints {
    opsworks = "https://opsworks.example.invalid"
  }
}

resource "aws_eip" "nat" {
  vpc = true
}

resource "aws_flow_log" "vpc" {
  log_group_name = "vpc-flow-logs"
  iam_role_arn   = "arn:aws:iam::123456789012:role/example"
  vpc_id         = "vpc-00000000000000000"
  traffic_type   = "ALL"
}

resource "aws_instance" "app" {
  ami                  = "ami-00000000000000000"
  instance_type        = "m6i.large"
  cpu_core_count       = 2
  cpu_threads_per_core = 1
  user_data            = "#!/bin/bash\necho hello"
}

resource "aws_launch_template" "workers" {
  name = "workers"

  # Exercised through a dynamic block on purpose: the flattened path has to come
  # out the same as the static form.
  dynamic "block_device_mappings" {
    for_each = var.volumes

    content {
      device_name = block_device_mappings.value.device_name

      ebs {
        encrypted = 1
      }
    }
  }
}

resource "aws_cur_report_definition" "billing" {
  report_name                = "monthly"
  time_unit                  = "DAILY"
  format                     = "textORcsv"
  compression                = "GZIP"
  additional_schema_elements = ["RESOURCES"]
  s3_bucket                  = "example-cur"
  s3_region                  = "eu-central-1"
}

resource "aws_opsworks_stack" "legacy" {
  name                         = "legacy"
  region                       = "eu-central-1"
  service_role_arn             = "arn:aws:iam::123456789012:role/opsworks"
  default_instance_profile_arn = "arn:aws:iam::123456789012:instance-profile/opsworks"
}

variable "volumes" {
  type    = list(object({ device_name = string }))
  default = []
}

module "network" {
  source = "./modules/network"
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.8.1"
}
