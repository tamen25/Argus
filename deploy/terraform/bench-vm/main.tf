# One EC2 instance that runs the flagship matrix on kind, unattended, so a run
# does not depend on the maintainer's PC staying on. Same environment as
# `make dev-up` (master plan §3.2: the full matrix runs on kind), on a VM.
#
# No inbound access: the instance is reached through SSM Session Manager and
# Run Command only. Results go to a private S3 bucket. The instance terminates
# itself when the run ends (instance_initiated_shutdown_behavior); `terraform
# destroy` removes the rest. See README.md and scripts/bench-vm.sh.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.region
  default_tags {
    tags = {
      Project = "argus"
      Purpose = "bench-vm"
    }
  }
}

data "aws_caller_identity" "current" {}

data "aws_ssm_parameter" "ubuntu" {
  name = "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  bucket = "${var.name}-${data.aws_caller_identity.current.account_id}-${var.region}"
}

# --- results ----------------------------------------------------------------

resource "aws_s3_bucket" "results" {
  bucket = local.bucket
  # Results are copied off before destroy; the bucket must not outlive the run.
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "results" {
  bucket                  = aws_s3_bucket.results.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# --- instance role: SSM, and this bucket only ---------------------------------

resource "aws_iam_role" "vm" {
  name = "${var.name}-vm"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.vm.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "results" {
  name = "results-bucket"
  role = aws_iam_role.vm.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = aws_s3_bucket.results.arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
        Resource = "${aws_s3_bucket.results.arn}/*"
      },
    ]
  })
}

resource "aws_iam_instance_profile" "vm" {
  name = "${var.name}-vm"
  role = aws_iam_role.vm.name
}

# --- network: its own VPC, public subnet, egress only ----------------------------
# Not the default VPC: an account may not have one. VPC, subnet and internet
# gateway cost nothing; there is no NAT gateway (the VM has a public IP for
# egress and no inbound rules).

resource "aws_vpc" "bench" {
  cidr_block           = "10.42.0.0/24"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = var.name }
}

resource "aws_internet_gateway" "bench" {
  vpc_id = aws_vpc.bench.id
  tags   = { Name = var.name }
}

resource "aws_subnet" "bench" {
  vpc_id            = aws_vpc.bench.id
  cidr_block        = "10.42.0.0/24"
  availability_zone = data.aws_availability_zones.available.names[0]
  tags              = { Name = var.name }
}

resource "aws_route_table" "bench" {
  vpc_id = aws_vpc.bench.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.bench.id
  }
  tags = { Name = var.name }
}

resource "aws_route_table_association" "bench" {
  subnet_id      = aws_subnet.bench.id
  route_table_id = aws_route_table.bench.id
}

resource "aws_security_group" "vm" {
  name        = "${var.name}-vm"
  description = "Argus bench VM: no inbound; reached through SSM"
  vpc_id      = aws_vpc.bench.id

  egress {
    description = "chart, image and model API downloads"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# --- the VM -------------------------------------------------------------------

resource "aws_instance" "vm" {
  ami                         = data.aws_ssm_parameter.ubuntu.value
  instance_type               = var.instance_type
  subnet_id                   = aws_subnet.bench.id
  associate_public_ip_address = true # egress without a NAT gateway; no inbound rules
  vpc_security_group_ids      = [aws_security_group.vm.id]
  iam_instance_profile        = aws_iam_instance_profile.vm.name

  # The run ends with `shutdown -h now`; terminating then stops the compute bill
  # even if nobody is watching.
  instance_initiated_shutdown_behavior = "terminate"

  metadata_options {
    http_tokens = "required"
  }

  root_block_device {
    volume_type           = "gp3"
    volume_size           = var.volume_gb
    delete_on_termination = true
  }

  depends_on = [aws_route_table_association.bench] # setup downloads at first boot

  user_data = templatefile("${path.module}/setup.sh.tftpl", {
    git_ref         = var.git_ref
    repo_url        = var.repo_url
    go_version      = var.go_version
    kind_version    = var.kind_version
    kubectl_version = var.kubectl_version
    helm_version    = var.helm_version
    codex_version   = var.codex_version
  })

  tags = {
    Name = "${var.name}-vm"
  }
}
