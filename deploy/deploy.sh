#!/bin/bash
# Atomix Demo 一键部署脚本（Ubuntu 24.04 + Docker + nginx HTTPS）
# 能力：拉取最新代码 → 注入 git SHA 构建镜像 → 强制非默认 JWT 密钥 → nginx 443 HTTPS 反代
set -e

APP_DIR=/opt/atomix-demo
DATA_DIR=/opt/atomix-data
ENV_FILE=$APP_DIR/.env
CONTAINER=atomix-demo
GIT_SHA=$(cd $APP_DIR && git rev-parse --short HEAD)

echo "==> 当前部署提交: $GIT_SHA"

cd $APP_DIR
echo "==> 拉取最新代码…"
git pull --ff-only

# ---------- JWT 密钥安全检查：拒绝默认密钥进入任何模式 ----------
if [ ! -f "$ENV_FILE" ]; then
  echo "!! 缺少 $ENV_FILE，请先创建（至少包含 DEEPSEEK_API_KEY）"
  exit 1
fi
if grep -q '^ATOMIX_JWT_SECRET=$' "$ENV_FILE" 2>/dev/null || ! grep -q '^ATOMIX_JWT_SECRET=' "$ENV_FILE"; then
  echo "==> 生成/更新随机 ATOMIX_JWT_SECRET（拒绝默认密钥）…"
  SECRET=$(openssl rand -hex 32)
  # 原地更新或追加，不覆盖其他键
  if grep -q '^ATOMIX_JWT_SECRET=' "$ENV_FILE"; then
    sed -i "s|^ATOMIX_JWT_SECRET=.*|ATOMIX_JWT_SECRET=$SECRET|" "$ENV_FILE"
  else
    echo "ATOMIX_JWT_SECRET=$SECRET" >> "$ENV_FILE"
  fi
  chmod 600 "$ENV_FILE"
fi
if grep -q '^ATOMIX_JWT_SECRET=atomix-demo-dev-secret-please-change' "$ENV_FILE"; then
  echo "!! 安全检查失败：.env 中仍是默认 JWT 密钥，拒绝部署"
  exit 1
fi
if ! grep -q '^ATOMIX_DEPLOY_SHA=' "$ENV_FILE"; then
  echo "ATOMIX_DEPLOY_SHA=" >> "$ENV_FILE"   # 占位：每次部署由下方 sed 写入本次 SHA
fi
sed -i "s|^ATOMIX_DEPLOY_SHA=.*|ATOMIX_DEPLOY_SHA=$GIT_SHA|" "$ENV_FILE"

# ---------- 构建镜像（SHA 经 build-arg 注入二进制，health 可核对） ----------
echo "==> 构建镜像（SHA=$GIT_SHA）…"
docker build --build-arg ATOMIX_DEPLOY_SHA=$GIT_SHA -t atomix-demo:latest .

# ---------- 运行容器：仅绑定 127.0.0.1:8080，由 nginx 统一对外 ----------
echo "==> 重建容器…"
docker rm -f $CONTAINER 2>/dev/null || true
docker run -d --name $CONTAINER \
  -p 127.0.0.1:8080:8080 \
  -v $DATA_DIR:/app/data \
  --env-file $ENV_FILE \
  --restart unless-stopped \
  atomix-demo:latest

# ---------- 健康自检（核对 SHA 与运行状态） ----------
sleep 2
HEALTH=$(curl -s http://127.0.0.1:8080/api/health || true)
echo "==> 健康检查: $HEALTH"
if ! echo "$HEALTH" | grep -q "\"sha\":\"$GIT_SHA\""; then
  echo "!! 健康检查 SHA 不匹配（期望 $GIT_SHA），请人工排查"
  exit 1
fi

# ---------- nginx HTTPS（自签证书 + 80 跳转 443） ----------
if ! command -v nginx >/dev/null 2>&1; then
  echo "==> 安装 nginx…"
  apt-get update -qq && apt-get install -y -qq nginx
fi

CERT_DIR=/etc/nginx/ssl
CERT=$CERT_DIR/atomix.crt
KEY=$CERT_DIR/atomix.key
if [ ! -f "$CERT" ]; then
  echo "==> 生成自签 TLS 证书（有效期 10 年）…"
  mkdir -p $CERT_DIR
  openssl req -x509 -nodes -newkey rsa:2048 -days 3650 \
    -keyout $KEY -out $CERT \
    -subj "/CN=atomix.local" \
    -addext "subjectAltName=DNS:atomix.local,IP:101.32.28.8"
fi

cat > /etc/nginx/conf.d/atomix.conf <<'NGINX'
# Atomix Demo：HTTPS 反代（容器仅监听 127.0.0.1:8080）
server {
    listen 80;
    server_name _;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name _;

    ssl_certificate     /etc/nginx/ssl/atomix.crt;
    ssl_certificate_key /etc/nginx/ssl/atomix.key;
    ssl_protocols TLSv1.2 TLSv1.3;

    # SSE 反代必需：关闭缓冲，保持长连接
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 3600s;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
NGINX

# 释放 80 端口（容器已不再直接占用；若 docker-proxy 仍监听 0.0.0.0:80 则已被上方重建解决）
nginx -t && (systemctl is-active nginx >/dev/null 2>&1 && systemctl reload nginx || systemctl enable --now nginx)

echo "==> 部署完成：https://101.32.28.8 （SHA=$GIT_SHA）"
curl -sk https://127.0.0.1/api/health || true
