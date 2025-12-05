#!/usr/bin/env bash
set -euo pipefail

IMAGE="localhost:5000/loadgen:latest"
JOB_NAME=${JOB_NAME:-loadgen-bench}
NAMESPACE=${NAMESPACE:-default}
TARGET_URL=${TARGET_URL:-http://demo-client:8080}
QPS=${QPS:-50}
DURATION=${DURATION:-180s}
READ_RATIO=${READ_RATIO:-0.5}
KEYS=${KEYS:-500}
VALUE_SIZE=${VALUE_SIZE:-100}
WORKERS=${WORKERS:-10}
OUTPUT=${OUTPUT:-/results/results.json}

kubectl delete job "${JOB_NAME}" -n "${NAMESPACE}" --ignore-not-found

cat <<YAML | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: ${JOB_NAME}
  namespace: ${NAMESPACE}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: loadgen
          image: ${IMAGE}
          args:
            - --urls=${TARGET_URL}
            - --qps=${QPS}
            - --duration=${DURATION}
            - --keys=${KEYS}
            - --value-size=${VALUE_SIZE}
            - --read-ratio=${READ_RATIO}
            - --workers=${WORKERS}
            - --timeout=5s
            - --report-interval=5s
            - --output=${OUTPUT}
            - --stdout-json
          volumeMounts:
            - name: results
              mountPath: /results
      volumes:
        - name: results
          emptyDir: {}
YAML

kubectl wait --for=condition=complete job/${JOB_NAME} -n "${NAMESPACE}" --timeout=240s
# Fetch results
POD=$(kubectl get pod -n "${NAMESPACE}" -l job-name=${JOB_NAME} -o jsonpath='{.items[0].metadata.name}')
kubectl cp "${NAMESPACE}/${POD}:${OUTPUT}" "bench-results/${JOB_NAME}.json"
