#!/bin/bash
set -e
cd /home/mhankbarbar/zcode-proxy
pkill -9 -f zcode-proxy || true
sleep 1
./zcode-proxy serve > proxy.log 2>&1 &
PROXY_PID=$!
echo "Started zcode-proxy PID $PROXY_PID"
sleep 2

echo "=== Sending curl request to glm-5.3-flash ==="
curl -i -N http://127.0.0.1:8081/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer joydazo" \
  -d '{"model": "glm-5.3-flash", "messages": [{"role": "user", "content": "Hello! Reply with exactly: PONG GLM-5.3-FLASH"}], "stream": true}'

echo ""
echo "=== PROXY LOG ==="
cat proxy.log

kill $PROXY_PID 2>/dev/null || true
