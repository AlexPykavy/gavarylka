resource "aws_s3_bucket" "ansible" {
  bucket = "gavarylka-ansible-playbooks"
}

resource "aws_s3_bucket_public_access_block" "ansible" {
  bucket = aws_s3_bucket.ansible.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "ansible" {
  bucket = aws_s3_bucket.ansible.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_vpc_endpoint" "s3" {
  vpc_id = aws_vpc.this.id

  service_name      = "com.amazonaws.${var.aws_region}.s3"
  vpc_endpoint_type = "Gateway"

  route_table_ids = [
    aws_route_table.private.id
  ]
}

resource "aws_s3_object" "playbooks" {
  for_each = fileset("${path.module}/../ansible", "**")

  bucket = aws_s3_bucket.ansible.id
  key    = "playbooks/${each.value}"
  source = "${path.module}/../ansible/${each.value}"

  etag = filemd5("${path.module}/../ansible/${each.value}")
}
