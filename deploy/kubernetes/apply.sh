#!/bin/bash
# Render the manifests, run the migration Job, then roll the Deployments.
# Edit deploy/kubernetes/components/images/kustomization.yaml before the first run.
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"

render() {
  kubectl kustomize deploy/kubernetes
}

select_kinds() {
  awk -v kinds="$1" '
    function emit(doc) {
      if (doc ~ ("kind: (" kinds ")")) {
        printf "%s", doc
        if (doc !~ /\n$/) {
          printf "\n"
        }
        printf "---\n"
      }
    }
    BEGIN { doc = "" }
    /^---[[:space:]]*$/ { emit(doc); doc = ""; next }
    { doc = doc $0 "\n" }
    END { emit(doc) }
  '
}

if kubectl get namespace music >/dev/null 2>&1; then
  kubectl delete job -n music music-migrate --ignore-not-found
fi

render | select_kinds "Namespace|ConfigMap|Secret|Job" | kubectl apply -f -
kubectl wait -n music --for=condition=complete job/music-migrate --timeout=180s
render | select_kinds "Deployment|Service|Ingress" | kubectl apply -f -
kubectl rollout status -n music deployment/music-api
kubectl rollout status -n music deployment/music-worker
kubectl rollout status -n music deployment/music-web
