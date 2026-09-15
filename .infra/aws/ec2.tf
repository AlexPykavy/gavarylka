data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }

  filter {
    name   = "name"
    values = ["al2023-ami-2023*"]
  }
}

resource "tls_private_key" "this" {
  algorithm = "ED25519"
}

resource "local_file" "private_key_file" {
  content         = tls_private_key.this.private_key_openssh
  filename        = "${path.module}/id_ed25519.pem"
  file_permission = "0400"
}

resource "aws_key_pair" "this" {
  key_name   = "terraform-generated-key"
  public_key = tls_private_key.this.public_key_openssh
}

resource "aws_iam_role" "this" {
  name = "ansible-runner"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"

    Statement = [{
      Effect = "Allow"

      Principal = {
        Service = "ec2.amazonaws.com"
      }

      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "this" {
  role = aws_iam_role.this.id

  policy = jsonencode({
    Version = "2012-10-17"

    Statement = [{
      Effect = "Allow"

      Action = [
        "s3:ListBucket"
      ]

      Resource = aws_s3_bucket.ansible.arn
      }, {
      Effect = "Allow"

      Action = [
        "s3:GetObject"
      ]

      Resource = "${aws_s3_bucket.ansible.arn}/playbooks/*"
    }]
  })
}

resource "aws_iam_instance_profile" "this" {
  name = "gavarylka-perf-lab"
  role = aws_iam_role.this.name
}

resource "aws_instance" "this" {
  ami = data.aws_ami.al2023.id

  associate_public_ip_address = true
  iam_instance_profile        = aws_iam_instance_profile.this.name
  instance_type               = "c8i.2xlarge"
  key_name                    = aws_key_pair.this.key_name
  subnet_id                   = aws_subnet.public.2.id
  vpc_security_group_ids = [
    aws_security_group.this.id
  ]
  user_data = templatefile("${path.module}/user-data.sh.tftpl", {
    bucket = aws_s3_bucket.ansible.id
  })

  instance_market_options {
    market_type = "spot"
    spot_options {
      max_price = 0.2
    }
  }

  tags = {
    Name = "gavarylka-perf-lab"
  }
}

output "instance_public_ip" {
  value = aws_instance.this.public_ip
}

output "ssh_connection_command" {
  value = "ssh -i ./id_ed25519.pem ec2-user@${aws_instance.this.public_ip}"
}
