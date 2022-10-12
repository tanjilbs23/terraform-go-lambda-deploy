resource "null_resource" "lambda_build" {
  triggers = {
    always_run = "${timestamp()}"
  }
  # triggers = {
  #   on_every_apply = uuid()
  # }
  provisioner "local-exec" {
    command = "cd ${path.module}/src && env GOOS=linux GOARCH=amd64 go build -o ../bin/handler"
  }
}

data "archive_file" "lambda_go_zip" {

  type        = "zip"
  source_file = "${path.module}/bin/handler"
  output_path = "${path.module}/bin/handler.zip"
  depends_on = [
    null_resource.lambda_build
  ]
}

# Lambda Module
module "lambda_function" {
  source        = "terraform-aws-modules/lambda/aws"
  function_name = "handler"
  description   = "testing go function"
  handler       = "handler.lambda_handler"
  runtime       = "go1.x"

  create_package         = false
  local_existing_package = "${path.module}/bin/handler.zip"

  
  ignore_source_code_hash = true
  

  trusted_entities = [
    {
      type = "Service",
      identifiers = [
        "appsync.amazonaws.com"
      ]
    }
  ]

  tags = {
    Name = var.tags
  }

  depends_on = [
    data.archive_file.lambda_go_zip
  ]
}