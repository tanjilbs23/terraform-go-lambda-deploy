# Building GO Lambda

resource "null_resource" "lambda_build" {
  provisioner "local-exec" {
    command = "export GO111MODULE=on"
  }

  provisioner "local-exec" {
    command = "cd src && env GOOS=linux GOARCH=amd64 go build -o ../bin/hello"
  }
}

# Lambda Module
module "lambda_function" {
    source = "terraform-aws-modules/lambda/aws"
    function_name = "hello"
    description   = "testing go function"
    handler       = "hello.lambda_handler"
    runtime       = "go1.x"

    source_path = "lambda/hello/bin"
    trusted_entities = [
    {
      type = "Service",
      identifiers = [
        "appsync.amazonaws.com"
      ]
    }
  ]

    tags = {
        Name = "Hello_GO"
    }
}