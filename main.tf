terraform {
  cloud {
    organization = "personal-testing-terraform"

    workspaces {
      name = "terraform-go-lambda-deploy"
    }
  }
  # required_providers {
  #   aws = {
  #     source  = "hashicorp/aws"
  #     version = "4.27.0"
  #   }
  # }
}

provider "aws" {
}

module "hello" {
  source = "./lambda/hello"
  tags   = "dev"
}


