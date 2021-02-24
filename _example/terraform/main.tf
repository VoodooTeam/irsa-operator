terraform {
  required_version = ">= 0.12.0"
}

provider "aws" {
  version = ">= 2.28.1"
  region  = var.region
}

provider "random" {
  version = "~> 2.1"
}

provider "local" {
  version = "~> 1.2"
}

provider "null" {
  version = "~> 2.1"
}

provider "template" {
  version = "~> 2.1"
}

data "aws_eks_cluster" "cluster" {
  name = module.eks.cluster_id
}

data "aws_eks_cluster_auth" "cluster" {
  name = module.eks.cluster_id
}

provider "kubernetes" {
  host                   = data.aws_eks_cluster.cluster.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.cluster.certificate_authority.0.data)
  token                  = data.aws_eks_cluster_auth.cluster.token
  load_config_file       = false
  version                = "~> 1.11"
}

data "aws_availability_zones" "available" {}

locals {
  cluster_name = "test-eks-${random_string.suffix.result}"
}

resource "random_string" "suffix" {
  length  = 8
  special = false
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "2.64.0"

  name                 = "test-irsa"
  cidr                 = "10.0.0.0/16"
  azs                  = data.aws_availability_zones.available.names
  private_subnets      = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
  public_subnets       = ["10.0.4.0/24", "10.0.5.0/24", "10.0.6.0/24"]
  enable_nat_gateway   = true
  single_nat_gateway   = true
  enable_dns_hostnames = true

  public_subnet_tags = {
    "kubernetes.io/cluster/${local.cluster_name}" = "shared"
    "kubernetes.io/role/elb"                      = "1"
  }

  private_subnet_tags = {
    "kubernetes.io/cluster/${local.cluster_name}" = "shared"
    "kubernetes.io/role/internal-elb"             = "1"
  }
}

module "eks" {
  source          = "terraform-aws-modules/eks/aws" 

  cluster_name    = local.cluster_name
  cluster_version = "1.18"
  subnets         = module.vpc.private_subnets
  enable_irsa     = true

  tags = {
    Environment = "test-irsa"
  }

  vpc_id = module.vpc.vpc_id

  worker_groups = [
    {
      name                          = "test-irsa"
      instance_type                 = "t2.small"
      asg_desired_capacity          = 1
    }
  ]
}

module "iam_assumable_role_admin" {
  source                        = "terraform-aws-modules/iam/aws//modules/iam-assumable-role-with-oidc"
  version = "3.6.0"
  create_role                   = true
  role_name                     = "irsa-operator"
  provider_url                  = replace(module.eks.cluster_oidc_issuer_url, "https://", "")
  role_policy_arns              = [aws_iam_policy.irsa.arn]
  oidc_fully_qualified_subjects = ["system:serviceaccount:irsa-operator-system:irsa-operator-oidc-sa"] # these fields are hardcoded in the helm chart
}

resource "aws_iam_policy" "irsa" {
  name_prefix = "irsa-operator"
  description = "irsa operator"
  policy      = data.aws_iam_policy_document.irsa.json
}

data "aws_iam_policy_document" "irsa" {
  statement {
    sid    = "irsaIam"
    effect = "Allow"

    actions = [
      "iam:*"
    ]

    resources = ["*"]
  }
}

module "s3_bucket" {
  source = "terraform-aws-modules/s3-bucket/aws"
  version          = "v1.17.0"

  bucket = "test-irsa-${lower(random_string.suffix.result)}"
  acl    = "private"
}

resource "aws_s3_bucket_object" "hello" {
  bucket = module.s3_bucket.this_s3_bucket_id
  key    = "/hello/" 
}

resource "aws_s3_bucket_object" "irsa" {
  bucket = module.s3_bucket.this_s3_bucket_id
  key    = "/irsa/" 
}

resource "aws_ecr_repository" "this" {
  name                 = "irsa"
}
