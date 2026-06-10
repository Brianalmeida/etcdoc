#!/bin/bash
# simulate-partition.sh
# This script simulates a network partition on an etcd member by dropping client and peer traffic.

# Ensure we're running as root (needed for iptables)
if [ "$EUID" -ne 0 ]; then
  echo "Please run as root"
  exit 1
fi

ACTION=$1
DURATION=${2:-60}

if [ "$ACTION" == "start" ]; then
    echo "Simulating network partition for $DURATION seconds by dropping port 2379 and 2380 traffic..."
    iptables -A INPUT -p tcp --dport 2379 -j DROP
    iptables -A INPUT -p tcp --dport 2380 -j DROP
    
    # Run in background to revert automatically
    (
        sleep $DURATION
        echo "Automatically reverting network partition..."
        iptables -D INPUT -p tcp --dport 2379 -j DROP
        iptables -D INPUT -p tcp --dport 2380 -j DROP
    ) &
    
elif [ "$ACTION" == "stop" ]; then
    echo "Reverting network partition immediately..."
    iptables -D INPUT -p tcp --dport 2379 -j DROP
    iptables -D INPUT -p tcp --dport 2380 -j DROP
else
    echo "Usage: $0 {start|stop} [duration_seconds]"
    echo "Example: $0 start 120"
    exit 1
fi
