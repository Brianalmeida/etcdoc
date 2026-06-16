#!/bin/bash
# longevity-test.sh
# A script to monitor etcdoc deployment health over a long period.
# Usage: ./longevity-test.sh [namespace] [duration_in_seconds] [check_interval_seconds]

NAMESPACE=${1:-kube-system}
DURATION=${2:-86400} # Default 1 day
INTERVAL=${3:-60}    # Default check every 60 seconds

echo "Starting longevity test for etcdoc in namespace: $NAMESPACE"
echo "Duration: $DURATION seconds, Interval: $INTERVAL seconds"

END_TIME=$(( $(date +%s) + $DURATION ))

FAILS=0
MAX_FAILS=5

while [ $(date +%s) -lt $END_TIME ]; do
    echo "--- Checking at $(date) ---"
    
    # 1. Check pod status
    echo "Pod Status:"
    kubectl get pods -n $NAMESPACE -l app=etcdoc -o wide
    
    # Check for restarts > 0 or not Running
    UNHEALTHY=$(kubectl get pods -n $NAMESPACE -l app=etcdoc --no-headers | awk '{if ($3 != "Running" || $4 > 0) print $1}')
    if [ ! -z "$UNHEALTHY" ]; then
        echo "WARNING: Unhealthy pods detected (restarts > 0 or not Running):"
        echo "$UNHEALTHY"
        FAILS=$((FAILS+1))
    else
        echo "All pods are Running with 0 restarts."
    fi

    # 2. Check metrics endpoint (port forward one pod briefly)
    POD_NAME=$(kubectl get pods -n $NAMESPACE -l app=etcdoc --no-headers | awk 'NR==1{print $1}')
    if [ ! -z "$POD_NAME" ]; then
        echo "Testing metrics endpoint on $POD_NAME..."
        kubectl port-forward -n $NAMESPACE pod/$POD_NAME 8081:8081 >/dev/null 2>&1 &
        PF_PID=$!
        sleep 2
        
        METRICS=$(curl -s -f http://localhost:8081/metrics || echo "")
        if [ ! -z "$METRICS" ]; then
            EVALS=$(echo "$METRICS" | grep -v '#' | grep etcd_reliability_last_success_timestamp_seconds | awk '{print $2}')
            echo "Metrics reached. etcd_reliability_last_success_timestamp_seconds = $EVALS"
        else
            echo "WARNING: Failed to curl metrics endpoint."
            FAILS=$((FAILS+1))
        fi
        
        kill $PF_PID 2>/dev/null
        wait $PF_PID 2>/dev/null
    else
        echo "WARNING: No pods found!"
        FAILS=$((FAILS+1))
    fi
    
    if [ $FAILS -ge $MAX_FAILS ]; then
        echo "ERROR: Test failed due to too many errors ($FAILS)."
        exit 1
    fi
    
    sleep $INTERVAL
done

echo "Longevity test completed successfully."
exit 0
