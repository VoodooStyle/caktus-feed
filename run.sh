#!/data/data/com.termux/files/usr/bin/bash
# Keeps the bridge running; restarts it if it ever exits.
cd "$(dirname "$0")"
termux-wake-lock 2>/dev/null
while true; do
  ./bridge -match caktus >> bridge.log 2>&1
  echo "$(date) bridge exited ($?), restarting in 30s" >> bridge.log
  sleep 30
done
