#!/bin/bash
set -e
cd /opt/atomix-demo
echo "=== git pull ==="
git pull
git log --oneline -2
echo "=== building image (validates Go compile) ==="
docker build -t atomix-demo:new .
echo "=== swapping container ==="
SECRET_VAL=$(docker inspect atomix-demo --format '{{range .Config.Env}}{{println .}}{{end}}' | grep '^SECRET=' | cut -d= -f2- || true)
docker rm -f atomix-demo
docker run -d --name atomix-demo \
  --restart unless-stopped \
  -p 80:8080 \
  -e SECRET="$SECRET_VAL" \
  -v /opt/atomix-data:/app/data \
  atomix-demo:new
docker rmi atomix-demo:latest 2>/dev/null || true
docker tag atomix-demo:new atomix-demo:latest
docker rmi atomix-demo:new 2>/dev/null || true
sleep 3
docker ps --filter name=atomix-demo --format "{{.Names}} {{.Status}}"
echo "=== health ==="
curl -s -m 10 http://127.0.0.1/api/health
