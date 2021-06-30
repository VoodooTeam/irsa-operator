output "s3_name" {
  description = "s3 bucket name"
  value       = module.s3_bucket.this_s3_bucket_id
}

output "ecr_url" {
  description = "ecr url"
  value       = aws_ecr_repository.this.repository_url
}

output "oidc_arn" {
  description = "oidc server arn"
  value = module.eks.oidc_provider_arn
}

