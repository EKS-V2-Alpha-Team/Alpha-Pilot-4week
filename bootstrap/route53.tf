resource "aws_route53_zone" "main" {
  name    = var.route53_hosted_zone_name
  comment = "${var.project_name} DNS, records managed by ExternalDNS"

  tags = {
    Name = "${var.project_name}-hosted-zone"
  }

  lifecycle {
    prevent_destroy = true
  }
}
