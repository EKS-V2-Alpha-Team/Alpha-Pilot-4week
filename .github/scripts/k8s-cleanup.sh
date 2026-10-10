#!/usr/bin/env bash
# Deletes what Kubernetes created in AWS for $CLUSTER_NAME, keeping each controller
# (LB controller, external-dns, EBS CSI, Karpenter) running until its resources are gone.
# Usage: k8s-cleanup.sh <step>. The K8s Cleanup workflow runs the steps in order.
set -eo pipefail
: "${CLUSTER_NAME:?}"
IDS_DIR=${IDS_DIR:-${RUNNER_TEMP:?}}

fail() { echo "::error::$*"; exit 1; }
has_crd() { kubectl get crd "$1" >/dev/null 2>&1; }
has_deploy() { kubectl -n "${1%/*}" get deploy "${1#*/}" >/dev/null 2>&1; }
ids() { tr '\t' '\n' | sed '/^$/d; /^None$/d'; }

# wait_for <what> <seconds> <lookup>: polls until <lookup> prints nothing.
# A failed lookup never counts as "nothing left".
wait_for() {
  local what=$1 deadline=$(( $(date +%s) + $2 )) fn=$3 left
  until left=$("$fn") && [ -z "$left" ]; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "::error::Timed out waiting for $what:"; echo "${left:-lookup failed}"; return 1
    fi
    echo "Waiting for $what"; sleep 15
  done
  echo "✅ $what: none left"
}

# --- lookups
appset_apps() {
  kubectl -n argocd get applications -o json \
    | jq -r '.items[] | select(any(.metadata.ownerReferences[]?; .kind == "ApplicationSet")) | .metadata.name'
}
lb_services() {
  kubectl get svc -A -o json \
    | jq -r '.items[] | select(.spec.type == "LoadBalancer") | "\(.metadata.namespace)/\(.metadata.name)"'
}
ebs_pvs() {
  kubectl get pv -o json | jq -r '.items[] | select(.spec.csi.driver == "ebs.csi.aws.com") | .metadata.name'
}
ebs_snapshots() {
  has_crd volumesnapshotcontents.snapshot.storage.k8s.io || return 0
  kubectl get volumesnapshotcontents -o json | jq -r '.items[] | select(.spec.driver == "ebs.csi.aws.com") | .metadata.name'
}
nodeclaims() { has_crd nodeclaims.karpenter.sh || return 0; kubectl get nodeclaims -o name; }
nodeclasses() { has_crd ec2nodeclasses.karpenter.k8s.aws || return 0; kubectl get ec2nodeclasses -o name; }
edns_args() { kubectl -n external-dns get deploy external-dns -o json | jq -r '.spec.template.spec.containers[0].args[]'; }

# TXT ownership records external-dns wrote for this cluster (their A records go with them)
dns_records() {
  [ -n "${DNS_ZONE_ID:-}" ] || return 0
  aws route53 list-resource-record-sets --hosted-zone-id "$DNS_ZONE_ID" --output json \
    | jq -r --arg o "$CLUSTER_NAME" '.ResourceRecordSets[] | select(.Type == "TXT")
        | select(any(.ResourceRecords[]?.Value; test("external-dns/owner=" + $o + "([,\"]|$)"))) | .Name'
}

# Everything Kubernetes made in AWS for this cluster. The account is shared by all
# environments, so each lookup filters on this cluster's tag, VPC or recorded IDs.
aws_leftovers() {
  local arns id out
  arns=$(aws elbv2 describe-load-balancers --query 'LoadBalancers[].LoadBalancerArn' --output text)
  if [ -n "$arns" ] && [ "$arns" != None ]; then
    xargs -n 20 aws elbv2 describe-tags --output json --resource-arns <<<"$arns" \
      | jq -r --arg c "$CLUSTER_NAME" '.TagDescriptions[]
          | select(any(.Tags[]; .Key == "elbv2.k8s.aws/cluster" and .Value == $c)) | "load-balancer \(.ResourceArn)"'
  fi
  aws ec2 describe-security-groups --filters "Name=tag:elbv2.k8s.aws/cluster,Values=$CLUSTER_NAME" \
    --query 'SecurityGroups[].GroupId' --output text | ids | sed 's/^/security-group /'
  aws ec2 describe-instances --filters "Name=tag:karpenter.sh/nodepool,Values=*" \
    "Name=tag-key,Values=kubernetes.io/cluster/$CLUSTER_NAME" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
    --query 'Reservations[].Instances[].InstanceId' --output text | ids | sed 's/^/karpenter-instance /'
  [ -s "$IDS_DIR/volumes.txt" ] && while read -r id; do
    if out=$(aws ec2 describe-volumes --volume-ids "$id" --query 'Volumes[0].State' --output text 2>&1); then
      [[ $out =~ ^delet ]] || echo "ebs-volume $id ($out)"
    elif ! grep -q InvalidVolume.NotFound <<<"$out"; then echo "ebs-volume $id (lookup failed)"; fi
  done < "$IDS_DIR/volumes.txt"
  [ -s "$IDS_DIR/snapshots.txt" ] && while read -r id; do
    if aws ec2 describe-snapshots --snapshot-ids "$id" >/dev/null 2>&1; then echo "ebs-snapshot $id"
    elif ! aws ec2 describe-snapshots --snapshot-ids "$id" 2>&1 | grep -q InvalidSnapshot.NotFound; then
      echo "ebs-snapshot $id (lookup failed)"; fi
  done < "$IDS_DIR/snapshots.txt"
  if [ -n "${VPC_ID:-}" ]; then
    # Cluster still up: its nodes' ENIs are in use, so only detached ones count.
    aws ec2 describe-network-interfaces --filters "Name=vpc-id,Values=$VPC_ID" "Name=status,Values=available" \
      --query 'NetworkInterfaces[].NetworkInterfaceId' --output text | ids | sed 's/^/detached-eni /'
  else
    # Cluster gone: any ENI the VPC CNI tagged for it is a leftover.
    aws ec2 describe-network-interfaces --filters "Name=tag:cluster.k8s.amazonaws.com/name,Values=$CLUSTER_NAME" \
      --query 'NetworkInterfaces[].NetworkInterfaceId' --output text | ids | sed 's/^/eni /'
  fi
  dns_records | sed 's/^/dns-record /'
}

# --- steps

# The zone comes from the running external-dns when there is one; its owner must be this
# cluster, or the records belong to another environment sharing the zone.
zone() {
  local zone="" owner zone_id
  if [ "${CLUSTER_EXISTS:-}" = true ] && has_deploy external-dns/external-dns; then
    owner=$(edns_args | sed -n 's/^--txt-owner-id=//p')
    zone=$(edns_args | sed -n 's/^--domain-filter=//p' | head -1)
    [ "$owner" = "$CLUSTER_NAME" ] || fail "external-dns labels records as '$owner', not '$CLUSTER_NAME'. Give this environment its own txtOwnerId first."
  fi
  [ -n "$zone" ] || zone=$(yq -r '.domainFilters[0] // ""' kubernetes/helm-values/external-dns-values.yaml)
  [ -n "$zone" ] || { echo "No external-dns zone, skipping DNS"; return 0; }
  zone_id=$(aws route53 list-hosted-zones-by-name --dns-name "$zone" --query "HostedZones[?Name=='$zone.'].Id | [0]" --output text)
  [ "$zone_id" != None ] || fail "Hosted zone $zone not found"
  echo "DNS_ZONE_ID=$zone_id" >> "$GITHUB_ENV"
}

# Read-only. Fails before anything is deleted if the cleanup couldn't finish.
preflight() {
  [ "$(kubectl auth can-i '*' '*' -A)" = yes ] || fail "Role isn't cluster-admin on $CLUSTER_NAME"

  # A controller only has to be up if something is waiting on it, otherwise that
  # object's finalizer can never complete.
  require() {
    if has_deploy "$1"; then kubectl -n "${1%/*}" rollout status "deploy/${1#*/}" --timeout=60s
    elif [ -n "$($2)" ]; then fail "$1 isn't installed but has resources to clean up"; fi
  }
  require kube-system/aws-load-balancer-controller lb_services
  require kube-system/karpenter nodeclaims
  require kube-system/ebs-csi-controller ebs_pvs
  require kube-system/snapshot-controller ebs_snapshots
  require external-dns/external-dns dns_records

  local keep
  keep=$(kubectl get pv -o json | jq -r '.items[] | select(.spec.persistentVolumeReclaimPolicy != "Delete") | .metadata.name')
  [ -z "$keep" ] || fail "These PVs would keep their EBS volume: $keep"
  if has_crd volumesnapshotcontents.snapshot.storage.k8s.io; then
    keep=$(kubectl get volumesnapshotcontents -o json | jq -r '.items[] | select(.spec.deletionPolicy != "Delete") | .metadata.name')
    [ -z "$keep" ] || fail "These snapshots would keep their EBS snapshot: $keep"
  fi

  # Deleting the NodePool would otherwise remove the node Karpenter runs on.
  local own
  own=$(kubectl get nodes -l karpenter.sh/nodepool -o name | sed 's|node/||')
  for n in $(kubectl -n kube-system get pods -l app.kubernetes.io/name=karpenter -o jsonpath='{.items[*].spec.nodeName}'); do
    ! grep -qx "$n" <<<"$own" || fail "Karpenter runs on its own node $n"
  done

  # EBS has no cluster tag to filter on, so record the exact IDs for the final check.
  kubectl get pv -o json | jq -r '.items[] | select(.spec.csi.driver == "ebs.csi.aws.com") | .spec.csi.volumeHandle' > "$IDS_DIR/volumes.txt"
  : > "$IDS_DIR/snapshots.txt"
  if has_crd volumesnapshotcontents.snapshot.storage.k8s.io; then
    kubectl get volumesnapshotcontents -o json \
      | jq -r '.items[] | select(.spec.driver == "ebs.csi.aws.com") | .status.snapshotHandle // empty' > "$IDS_DIR/snapshots.txt"
  fi
}

gitops() {
  # Root app first, or it recreates everything below. Strip its finalizer so it can't cascade.
  if kubectl -n argocd get application platform >/dev/null 2>&1; then
    kubectl -n argocd patch application platform --type merge -p '{"metadata":{"finalizers":null}}'
    kubectl -n argocd delete application platform --timeout=2m
  fi
  # Service apps have the resources finalizer, so deleting the ApplicationSet removes the services.
  kubectl -n argocd delete applicationsets --all --wait=false
  wait_for "service apps" 900 appset_apps
  # The rest have no finalizer: deleting them stops self-heal but leaves the controllers running.
  for app in $(kubectl -n argocd get applications -o name); do
    kubectl -n argocd patch "$app" --type merge -p '{"metadata":{"finalizers":null}}'
  done
  kubectl -n argocd delete applications --all --timeout=2m
}

# upsert-only never deletes records. The environment is going away, so switch the running
# external-dns to sync for the teardown. ArgoCD no longer manages it, so this sticks.
dns_sync() {
  has_deploy external-dns/external-dns || return 0
  local i
  i=$(kubectl -n external-dns get deploy external-dns -o json \
    | jq '.spec.template.spec.containers[0].args | map(startswith("--policy=")) | index(true)')
  [ "$i" != null ] && ! edns_args | grep -qx -- --policy=sync || return 0
  kubectl -n external-dns patch deploy external-dns --type json \
    -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/args/$i\",\"value\":\"--policy=sync\"}]"
  kubectl -n external-dns rollout status deploy/external-dns --timeout=3m
}

snapshots() {
  has_crd snapshotschedules.snapscheduler.backube && kubectl delete snapshotschedules --all -A --timeout=2m
  has_crd volumesnapshots.snapshot.storage.k8s.io || return 0
  kubectl delete volumesnapshots --all -A --wait=false
  wait_for "EBS snapshots" 900 ebs_snapshots
}

# A PVC can't go while a pod uses it, so remove the owners first (Prometheus' StatefulSet
# belongs to the operator). Postgres keeps its PVC on delete, so all PVCs are deleted
# explicitly. No final snapshot, by agreement.
volumes() {
  local kind ns
  for kind in prometheuses alertmanagers; do
    has_crd "$kind.monitoring.coreos.com" && kubectl delete "$kind" --all -A --timeout=5m
  done
  for ns in $(kubectl get pvc -A -o jsonpath='{range .items[*]}{.metadata.namespace}{"\n"}{end}' | sort -u); do
    kubectl -n "$ns" delete statefulsets --all --timeout=5m
  done
  kubectl delete pvc --all -A --wait=false
  wait_for "EBS volumes" 900 ebs_pvs
}

loadbalancers() {
  local svc
  for svc in $(lb_services); do kubectl -n "${svc%/*}" delete svc "${svc#*/}" --wait=false; done
  wait_for "load balancers" 900 lb_services
  wait_for "DNS records owned by $CLUSTER_NAME" 600 dns_records
}

# Last, so the controllers above aren't evicted mid-work.
karpenter() {
  has_crd nodepools.karpenter.sh && kubectl delete nodepools --all --wait=false
  wait_for "Karpenter nodes" 1200 nodeclaims
  has_crd ec2nodeclasses.karpenter.k8s.aws && kubectl delete ec2nodeclasses --all --wait=false
  wait_for "EC2NodeClasses" 600 nodeclasses
}

leftovers() { wait_for "AWS resources left by Kubernetes" 600 aws_leftovers; }

diagnostics() {
  set +e
  for kind in svc pvc pv volumesnapshotcontent nodeclaim application; do
    kubectl get "$kind" -A -o json 2>/dev/null | jq -r --arg k "$kind" '.items[] | select(.metadata.deletionTimestamp)
      | "stuck: \($k) \(.metadata.namespace // "-")/\(.metadata.name) \(.metadata.finalizers)"'
  done
  for d in kube-system/aws-load-balancer-controller external-dns/external-dns kube-system/karpenter; do
    kubectl -n "${d%/*}" logs "deploy/${d#*/}" --tail=30
  done
}

# Finds the cluster. MISSING = already destroyed, so only the AWS check runs (the run after
# Terraform Destroy). Any other non-ACTIVE state is mid-change.
check() {
  local status
  status=$(aws eks describe-cluster --name "$CLUSTER_NAME" --query cluster.status --output text 2>/dev/null || echo MISSING)
  echo "$CLUSTER_NAME is $status"
  case "$status" in
    ACTIVE)
      CLUSTER_EXISTS=true
      echo "exists=true" >> "$GITHUB_OUTPUT"
      { echo "CLUSTER_EXISTS=true"
        echo "VPC_ID=$(aws eks describe-cluster --name "$CLUSTER_NAME" --query cluster.resourcesVpcConfig.vpcId --output text)"
      } >> "$GITHUB_ENV"
      aws eks update-kubeconfig --name "$CLUSTER_NAME" ;;
    MISSING) echo "exists=false" >> "$GITHUB_OUTPUT" ;;
    *) fail "$CLUSTER_NAME is $status, re-run when it's finished" ;;
  esac
  zone
}

# Runs the deletions in order, each as its own collapsible section in the log.
teardown() {
  local step
  for step in gitops dns_sync snapshots volumes loadbalancers karpenter; do
    echo "::group::$step"
    "$step"
    echo "::endgroup::"
  done
}

# After the cluster is gone: EBS has no cluster tag, so use the IDs the last successful cleanup recorded.
fetch_ids() {
  local run
  for run in $(gh run list -R "$GITHUB_REPOSITORY" --workflow k8s-cleanup.yaml --status success \
                 --limit 20 --json databaseId --jq '.[].databaseId'); do
    if gh run download "$run" -R "$GITHUB_REPOSITORY" -n "k8s-ids-$ENVIRONMENT" -D "$IDS_DIR" 2>/dev/null; then
      echo "Checking the volume/snapshot IDs recorded by run $run"
      return 0
    fi
  done
  echo "::warning::No recorded IDs found, so EBS volumes and snapshots aren't checked"
}

case "${1:-}" in
  check|preflight|teardown|fetch_ids|leftovers|diagnostics|zone|gitops|dns_sync|snapshots|volumes|loadbalancers|karpenter) "$1" ;;
  *) fail "Unknown step '${1:-}'" ;;
esac
