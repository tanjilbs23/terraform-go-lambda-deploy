terraform {
  cloud {
    organization = "personal-testing-terraform"

    workspaces {
      name = "terraform-go-lambda-deploy"
    }

  }
}

provider "aws" {}

module "hello" {
  source = "./lambda/hello"
  tags   = var.tags
}

