# Building GO Lambda

resource "null_resource" "lambda_build" {
  # triggers = {
  #   always_run = "${timestamp()}"
  # }
  triggers = {
    on_every_apply = uuid()
  }
  provisioner "local-exec" {
    command = "cd lambda/hello/src && env GOOS=linux GOARCH=amd64 go build -o ../bin/hello"
  }
}

# Zip Lambda package

data "archive_file" "lambda_go_zip" {

  type        = "zip"
  source_file = "lambda/hello/bin/hello"
  output_path = "lambda/hello/bin/hello.zip"
  depends_on = [
      null_resource.lambda_build
  ]
}

# Lambda Module
module "lambda_function" {
  source        = "terraform-aws-modules/lambda/aws"
  function_name = "hello"
  description   = "testing go function"
  handler       = "hello.lambda_handler"
  runtime       = "go1.x"

  create_package          = false
  local_existing_package  = "lambda/hello/bin/hello.zip"

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

  depends_on = [
    data.archive_file.lambda_go_zip
  ]
}