#!/usr/bin/env bash
set -euo pipefail

CONTAINER=floci-local
BUCKET=alpha-local-tfstate

if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  echo "Starting Floci..."
  docker run --rm -d --name "$CONTAINER" \
    --user root \
    -p 4566:4566 \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -e FLOCI_SERVICES_UI_ENABLED=true \
    -e FLOCI_RUN_AS_ROOT=true \
    floci/floci:2.1.0-compat
fi

until curl -sf http://localhost:4566/_floci/init > /dev/null 2>&1; do
  echo "Waiting for Floci..."
  sleep 2
done

docker exec "$CONTAINER" aws s3api head-bucket --bucket "$BUCKET" 2>/dev/null \
  || docker exec "$CONTAINER" aws s3api create-bucket --bucket "$BUCKET" --region us-east-1

cd "$(dirname "$0")/../infra/terraform/local"
terraform init -upgrade
terraform plan "$@"

echo
echo "Review the plan above. Apply with: terraform -chdir=infra/terraform/local apply \"\$@\""
echo "UI: http://localhost:4566/_floci/ui"