output "instance_id" {
  description = "Target for SSM (scripts/bench-vm.sh)"
  value       = aws_instance.vm.id
}

output "bucket" {
  description = "Results bucket (destroyed with the stack: copy results off first)"
  value       = aws_s3_bucket.results.bucket
}

output "region" {
  value = var.region
}
