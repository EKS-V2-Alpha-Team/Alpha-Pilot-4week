resource "aws_db_instance" "this" {
  identifier                    = "${var.project_name}-${var.environment}-postgres"
  db_name                       = var.db_name
  engine                        = "postgres"
  engine_version                = var.engine_version
  auto_minor_version_upgrade    = true
  allow_major_version_upgrade   = var.allow_major_version_upgrade
  apply_immediately             = var.apply_immediately
  maintenance_window            = var.maintenance_window
  instance_class                = var.instance_class
  storage_type                  = "gp3"
  allocated_storage             = var.allocated_storage
  max_allocated_storage         = var.max_allocated_storage
  storage_encrypted             = true
  kms_key_id                    = var.kms_key_arn
  username                      = var.master_username
  manage_master_user_password   = true
  master_user_secret_kms_key_id = var.kms_key_arn
  port                          = 5432
  db_subnet_group_name          = var.db_subnet_group_name
  vpc_security_group_ids        = [aws_security_group.rds.id]
  publicly_accessible           = false
  multi_az                      = var.multi_az


  backup_retention_period = var.backup_retention_period
  backup_window           = var.backup_window
  copy_tags_to_snapshot   = true

  deletion_protection       = var.deletion_protection
  delete_automated_backups  = var.delete_automated_backups
  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.project_name}-${var.environment}-postgres-final"

  performance_insights_enabled    = var.performance_insights_enabled
  enabled_cloudwatch_logs_exports = ["postgresql"]

  tags = {
    Name = "${var.project_name}-postgres"
  }

  depends_on = [aws_cloudwatch_log_group.postgres]

  lifecycle {

    precondition {
      condition = var.environment != "prod" || (
        var.multi_az &&
        var.deletion_protection &&
        !var.skip_final_snapshot &&
        var.backup_retention_period >= 7 &&
        !startswith(var.instance_class, "db.t")
      )
      error_message = "prod requires multi_az, deletion_protection, a final snapshot, >= 7 days of backups, and a non-burstable instance class."
    }
  }
}
