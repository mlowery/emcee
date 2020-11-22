#!/usr/bin/env bash

set -euo pipefail

f=$(kubectl config current-context)-out

# env var KUBECONFIG has been set by emcee
kubectl get po &> $f

#cat ${KUBECONFIG} >> out
