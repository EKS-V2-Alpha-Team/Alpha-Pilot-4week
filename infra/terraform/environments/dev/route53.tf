data "aws_route53_zone" "main" {
  name         = var.route53_hosted_zone_name
  private_zone = false
}
