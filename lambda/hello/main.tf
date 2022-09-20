# Building GO Lambda

resource "null_resource" "lambda_build" {
  provisioner "local-exec" {
    command = "cd src && env GOOS=linux GOARCH=amd64 go build -o ../bin/hello"
  }
}

# Zipping Lambda package

data "archive_file" "lambda_go_zip" {
  type        = "zip"
  source_file = "../bin/hello"
  output_path = "bin/hello.zip"
}

# Lambda Module
module "lambda_function" {
    source = "terraform-aws-modules/lambda/aws"
    function_name = "hello"
    description   = "testing go function"
    handler       = "hello.lambda_handler"
    runtime       = "go1.x"
     
    create_package         = false
    local_existing_package = "./bin/hello.zip"
    ignore_source_code_hash = true

    # source_path = "lambda/hello/bin"
    trusted_entities = [
    {
      type = "Service",
      identifiers = [
        "appsync.amazonaws.com"
      ]
    }
  ]

    tags = {
        Name = "hello_go"
    }
}