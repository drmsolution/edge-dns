#!/usr/bin/env bash
# Edge DNS — Single VPS Deployment Script
set -euo pipefail

# ─── Config ───────────────────────────────────────────────────────────────────
DOMAIN="${DOMAIN:-dns.example.com}"
EMAIL="${EMAIL:-admin@example.com}"
GITHUB_REPO="${GITHUB_REPO:-drmsolution/edge-dns}"
INSTALL_DIR="${INSTALL_DIR:-/opt/edge-dns}"

# ─── Color ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# ─── Pre-flight ───────────────────────────────────────────────────────────────
[ "$EUID" -ne 0 ] && error "Chạy với root (sudo su)"

usage() {
  cat <<EOF
Triển khai Edge DNS trên 1 VPS (Docker Compose).

Cách chạy:
  export DOMAIN=dns.example.com
  export EMAIL=admin@example.com
  sudo -E bash deploy.sh

Biến môi trường tuỳ chọn:
  DOMAIN        - Domain cho DoH/Admin API
  EMAIL         - Email Let's Encrypt
  INSTALL_DIR   - Thư mục cài đặt (mặc định: /opt/edge-dns)
  GITHUB_REPO   - GitHub repo (mặc định: drmsolution/edge-dns)
EOF
  exit 0
}

[ "${1:-}" = "--help" ] && usage

# ─── Step 1: System prepare ───────────────────────────────────────────────────
step_system() {
  info "[1/6] Cập nhật hệ thống và cài đặt packages..."

  OS_CODENAME=$(grep VERSION_CODENAME /etc/os-release | cut -d= -f2)
  if [ "$OS_CODENAME" = "focal" ]; then
    warn "Ubuntu 20.04 (focal) đã EOL — khuyến nghị nâng cấp lên Ubuntu 22.04+"
    warn "Script vẫn cố cài tiếp nhưng có thể gặp lỗi apt."
  fi

  apt update -qq && apt upgrade -y -qq
  apt install -y -qq curl gnupg apt-transport-https ca-certificates \
    git jq htop netcat-openbsd ufw

  # Tắt swap
  swapoff -a; sed -i '/ swap / s/^/#/' /etc/fstab

  # Kernel modules cho DNS
  cat > /etc/modules-load.d/edge-dns.conf <<'EOF'
nf_conntrack
EOF
  modprobe nf_conntrack || true

  # Sysctl cho DNS server
  cat > /etc/sysctl.d/99-edge-dns.conf <<'EOF'
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.ipv4.udp_mem = 65536 131072 262144
net.core.somaxconn = 65535
net.ipv4.tcp_fastopen = 3
net.ipv4.ip_local_port_range = 1024 65535
EOF
  sysctl --system
}

# ─── Step 2: Docker + Compose ────────────────────────────────────────────────
step_docker() {
  info "[2/6] Cài Docker + Compose plugin..."

  if command -v docker &>/dev/null; then
    info "Docker đã có, bỏ qua"
    systemctl enable --now docker
    return
  fi

  OS_CODENAME=$(grep VERSION_CODENAME /etc/os-release | cut -d= -f2)
  if [ "$OS_CODENAME" = "focal" ]; then
    warn "Ubuntu 20.04 (focal) đã EOL — Docker apt repo không còn hỗ trợ chính thức."
    warn "Vẫn cài được nhưng sẽ dùng Docker package từ archive. Nếu lỗi, hãy nâng cấp lên Ubuntu 22.04+"
  fi

  # Cài Docker từ apt repo chính thức (không dùng get.docker.com convenience script)
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/ubuntu/gpg" -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $OS_CODENAME stable" \
    > /etc/apt/sources.list.d/docker.list

  apt update -qq
  DEBIAN_FRONTEND=noninteractive apt install -y -qq \
    docker-ce docker-ce-cli containerd.io \
    docker-compose-plugin docker-buildx-plugin

  systemctl enable --now docker
}

# ─── Step 3: Clone repo ─────────────────────────────────────────────────────
step_clone() {
  info "[3/6] Clone repo vào $INSTALL_DIR..."

  if [ -d "$INSTALL_DIR/.git" ]; then
    info "Repo đã tồn tại, pull cập nhật..."
    cd "$INSTALL_DIR" && git pull
  else
    rm -rf "$INSTALL_DIR"
    git clone "https://github.com/$GITHUB_REPO.git" "$INSTALL_DIR"
  fi
  cd "$INSTALL_DIR"
}

# ─── Step 4: Build images ────────────────────────────────────────────────────
step_build() {
  info "[4/6] Build Docker images..."

  cd "$INSTALL_DIR"
  docker compose build \
    --build-arg GOPROXY=https://proxy.golang.org,direct
}

# ─── Step 5: Firewall ────────────────────────────────────────────────────────
step_firewall() {
  info "[5/6] Cấu hình firewall (ufw)..."

  ufw --force reset
  ufw default deny incoming
  ufw default allow outgoing

  # SSH
  ufw allow ssh

  # DNS queries
  ufw allow 8053/tcp comment 'DNS-over-TCP'
  ufw allow 8053/udp comment 'DNS-over-UDP'
  ufw allow 8443/tcp comment 'DNS-over-HTTPS'
  ufw allow 8853/tcp comment 'DNS-over-TLS'

  # Admin API + SNI proxy
  ufw allow 8080/tcp comment 'Admin API'
  ufw allow 443/tcp comment 'SNI Proxy'

  # Monitoring
  ufw allow 2112/tcp comment 'Prometheus metrics'

  # Node exporter (nếu có)
  ufw allow 9100/tcp comment 'Node exporter'

  ufw --force enable
}

# ─── Step 6: Start services ──────────────────────────────────────────────────
step_start() {
  info "[6/6] Khởi động services..."

  cd "$INSTALL_DIR"

	# Tạo .env nếu chưa có
	if [ ! -f .env ]; then
		# Sinh ADMIN_API_KEY ngẫu nhiên nếu chưa set
		if [ -z "${ADMIN_API_KEY:-}" ]; then
			ADMIN_API_KEY=$(openssl rand -hex 32 2>/dev/null || head -c 64 /dev/urandom | base64 -w0)
		fi

		cat > .env <<EOF
# Edge DNS Configuration
DOMAIN=$DOMAIN
ADMIN_API_ADDR=:8080
ADMIN_API_KEY=$ADMIN_API_KEY
SNI_PROXY_ADDR=:443
STD_ADDR=:8053
DOH_ADDR=:8443
DOT_ADDR=:8853
METRICS_ADDR=:2112
LOG_LEVEL=info
RATE_LIMIT_QUERIES=100
UPSTREAM_DNS=1.1.1.1:53
UPSTREAM_TIMEOUT=5s
EOF
		info "Admin API key: $ADMIN_API_KEY — hãy lưu lại!"
	fi

  docker compose up -d

  info "Đợi services ready..."
  sleep 5

  # Kiểm tra từng service
  for svc in redis clickhouse edge-dns admin-api sniproxy; do
    if docker compose ps "$svc" --format json | grep -q '"Status":"running'; then
      info "  ✓ $svc running"
    else
      warn "  ✗ $svc NOT running — kiểm tra: docker compose logs $svc"
    fi
  done
}

# ─── Optional: Nginx + SSL (Let's Encrypt) ──────────────────────────────────
step_ssl() {
  info "[Tùy chọn] Cài Nginx reverse proxy + Let's Encrypt SSL..."

  apt install -y -qq nginx certbot python3-certbot-nginx

  # Nginx config cho DoH reverse proxy
  cat > /etc/nginx/sites-available/edge-dns <<EOF
server {
    listen 80;
    server_name $DOMAIN api.$DOMAIN;
    return 301 https://\$server_name\$request_uri;
}

server {
    listen 443 ssl http2;
    server_name $DOMAIN;

    location /dns-query {
        proxy_pass http://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
    }

    location /healthz {
        proxy_pass http://127.0.0.1:2112/healthz;
    }

    location /readyz {
        proxy_pass http://127.0.0.1:2112/readyz;
    }
}

server {
    listen 443 ssl http2;
    server_name api.$DOMAIN;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    }
}
EOF

	# Ghi cert paths vào .env cho edge-dns và sniproxy dùng
	if [ -f /etc/letsencrypt/live/"$DOMAIN"/fullchain.pem ]; then
		sed -i '/^TLS_CERT_FILE=/d' "$INSTALL_DIR/.env"
		sed -i '/^TLS_KEY_FILE=/d' "$INSTALL_DIR/.env"
		cat >> "$INSTALL_DIR/.env" <<EOF
TLS_CERT_FILE=/etc/letsencrypt/live/$DOMAIN/fullchain.pem
TLS_KEY_FILE=/etc/letsencrypt/live/$DOMAIN/privkey.pem
EOF
		# Restart để nhận cert mới
		cd "$INSTALL_DIR" && docker compose up -d
	fi

	# Certbot
	certbot --nginx -d "$DOMAIN" -d "api.$DOMAIN" --non-interactive \
		--agree-tos --email "$EMAIL" --redirect || {
		warn "certbot thất bại — kiểm tra DNS A record đã trỏ đến IP VPS chưa?"
		warn "Chạy thủ công sau: certbot --nginx -d $DOMAIN -d api.$DOMAIN"
	}

	# Auto-renew cert
	systemctl enable --now certbot.timer

  # Kích hoạt site
  ln -sf /etc/nginx/sites-available/edge-dns /etc/nginx/sites-enabled/
  rm -f /etc/nginx/sites-enabled/default
  nginx -t && systemctl reload nginx
}

# ─── Optional: Systemd service ──────────────────────────────────────────────
step_systemd() {
  info "[Tùy chọn] Cài systemd service cho auto-start..."

  cat > /etc/systemd/system/edge-dns.service <<'EOF'
[Unit]
Description=Edge DNS (Docker Compose)
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/edge-dns
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
StandardOutput=journal

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable edge-dns.service
  systemctl start edge-dns.service
}

# ─── Info ────────────────────────────────────────────────────────────────────
step_info() {
	info "=== Triển khai hoàn tất ==="
	echo ""

	if [ -f "$INSTALL_DIR/.env" ]; then
		source <(grep ADMIN_API_KEY "$INSTALL_DIR/.env")
	fi
	if [ -n "${ADMIN_API_KEY:-}" ]; then
		echo "Admin API key:  $ADMIN_API_KEY"
		echo ""
	fi

	echo "DNS query:      dig @$(curl -4s ifconfig.me) -p 8053 google.com"
	echo "DoH:            curl -s 'https://$DOMAIN/dns-query?name=google.com&type=A' -H 'Accept: application/dns-json'"
	echo "DoT:            kdig @$DOMAIN -p 8853 +tls google.com"
	echo "Admin API:      curl -H 'Authorization: Bearer \$ADMIN_API_KEY' http://127.0.0.1:8080/api/v1/rules | jq ."
	echo "Health check:   curl http://127.0.0.1:2112/healthz"
	echo "Ready check:    curl http://127.0.0.1:2112/readyz"
	echo ""
	echo "Logs:           cd $INSTALL_DIR && docker compose logs -f"
	echo "Restart:        cd $INSTALL_DIR && docker compose restart"
	echo "Update:         cd $INSTALL_DIR && git pull && docker compose build && docker compose up -d"
}

# ─── Main ────────────────────────────────────────────────────────────────────
main() {
  echo "=============================================="
  echo "  Edge DNS — Single VPS Deployment"
  echo "=============================================="
  echo "DOMAIN:       $DOMAIN"
  echo "EMAIL:        $EMAIL"
  echo "Install dir:  $INSTALL_DIR"
  echo ""

  step_system
  step_docker
  step_clone
  step_build
  step_firewall
  step_start

  # Tuỳ chọn — bỏ comment nếu muốn
  # step_ssl
  # step_systemd

  step_info
}

main "$@"
