#!/usr/bin/env bash
# =============================================================================
# Edge DNS — Production Setup Script
# Chạy trên từng VPS theo role được gán (master | worker1 | worker2 | worker3)
# =============================================================================
set -euo pipefail

# ─── CONFIG ─────────────────────────────────────────────────────────────────
# ĐIỀN CÁC GIÁ TRỊ CỦA BẠN VÀO ĐÂY
export VPS_ROLE="${1:-}"            # master | worker1 | worker2 | worker3
export MASTER_IP=""                 # IP VPS 1
export K3S_TOKEN=""                 # Lấy sau khi chạy master
export DOMAIN="dns.example.com"     # domain cho DoH/DoT
export EMAIL="admin@example.com"    # email Let's Encrypt
export GITHUB_REPO="drmsolution/edge-dns"
export IMAGE_TAG="latest"
# ──────────────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

usage() {
  cat <<EOF
Usage: $0 <role>

Roles:
  master    — k3s master + control-plane + admin-api + grafana
  worker1   — k3s worker + edge-dns + redis
  worker2   — k3s worker + edge-dns + clickhouse
  worker3   — k3s worker + edge-dns + prometheus

Example:
  export MASTER_IP=1.2.3.4
  export DOMAIN=dns.example.com
  export EMAIL=admin@example.com
  $0 master          # chạy trên VPS 1
  $0 worker1         # chạy trên VPS 2
  $0 worker2         # chạy trên VPS 3
  $0 worker3         # chạy trên VPS 4
EOF
  exit 1
}

[ -z "$VPS_ROLE" ] && usage

# ─── Hàm chung ──────────────────────────────────────────────────────────────

system_prepare() {
  info "Cập nhật hệ thống..."
  apt update -qq && apt upgrade -y -qq
  apt install -y -qq curl gnupg apt-transport-https ca-certificates \
    git jq htop netcat-openbsd

  info "Tắt firewall (ufw) — sẽ dùng k3s network policy sau"
  ufw disable 2>/dev/null || true

  info "Tắt swap..."
  swapoff -a; sed -i '/ swap / s/^/#/' /etc/fstab

  info "Module kernel cho containerd..."
  cat > /etc/modules-load.d/k3s.conf <<EOF
overlay
br_netfilter
EOF
  modprobe overlay; modprobe br_netfilter
}

install_docker() {
  if ! command -v docker &>/dev/null; then
    info "Cài Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
  else
    info "Docker đã có, bỏ qua"
  fi
}

install_helm() {
  if ! command -v helm &>/dev/null; then
    info "Cài Helm..."
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  fi
}

wait_for_pods() {
  local ns="${1:-edge-dns}"
  info "Đợi tất cả pod trong namespace $ns ready..."
  kubectl wait --for=condition=Ready pods --all -n "$ns" --timeout=300s 2>/dev/null || true
  kubectl -n "$ns" get pods -o wide
}

# ─── Master ─────────────────────────────────────────────────────────────────

setup_master() {
  system_prepare
  install_docker

  info "Cài k3s master..."
  curl -sfL https://get.k3s.io | sh -s - \
    --write-kubeconfig-mode 644 \
    --disable traefik \
    --disable servicelb \
    --node-name master

  export K3S_TOKEN=$(sudo cat /var/lib/rancher/k3s/server/node-token)
  info "=== K3S TOKEN (dùng cho worker) ==="
  echo "$K3S_TOKEN"
  info "=== MASTER IP ==="
  echo "$MASTER_IP"

  mkdir -p ~/.kube
  sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
  sudo chown $(id -u):$(id -g) ~/.kube/config

  install_helm
  git clone https://github.com/$GITHUB_REPO.git ~/edge-dns || true
  cd ~/edge-dns

  # Label node cho scheduling
  kubectl label node master node-role.kubernetes.io/control-plane=true --overwrite

  # ─── Cài cert-manager cho SSL ───
  info "Cài cert-manager..."
  helm repo add jetstack https://charts.jetstack.io --force-update
  helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --set crds.enabled=true \
    --wait

  # ─── ClusterIssuer Let's Encrypt ───
  cat <<EOF | kubectl apply -f -
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: $EMAIL
    privateKeySecretRef:
      name: letsencrypt-prod-key
    solvers:
      - http01:
          ingress:
            class: nginx
EOF

  # ─── Nginx Ingress cho DoH ───
  info "Cài nginx-ingress..."
  helm upgrade --install ingress-nginx ingress-nginx \
    --repo https://kubernetes.github.io/ingress-nginx \
    --namespace ingress-nginx --create-namespace \
    --set controller.service.type=NodePort \
    --set controller.service.nodePorts.http=30080 \
    --set controller.service.nodePorts.https=30443 \
    --wait

  # ─── Deploy edge-dns với Helm ───
  info "Deploy edge-dns..."
  helm upgrade --install edge-dns deployments/helm/edge-dns/ \
    --namespace edge-dns --create-namespace \
    --set global.imageTag=$IMAGE_TAG \
    --set edge-dns.replicas=3 \
    --set monitoring.prometheus.enabled=true \
    --set monitoring.grafana.enabled=true \
    --set monitoring.prometheus.storageSize=10Gi \
    --set clickhouse.persistence.data.size=30Gi \
    --wait

  # ─── Ingress cho DoH + Admin API + Grafana ───
  cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: edge-dns-ingress
  namespace: edge-dns
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - $DOMAIN
      secretName: edge-dns-tls
  rules:
    - host: $DOMAIN
      http:
        paths:
          - path: /dns-query
            pathType: Prefix
            backend:
              service:
                name: edge-dns
                port:
                  number: 8443
EOF

  # ─── Ingress cho Admin API ───
  cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: admin-api-ingress
  namespace: edge-dns
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - "api.$DOMAIN"
      secretName: admin-api-tls
  rules:
    - host: "api.$DOMAIN"
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: admin-api
                port:
                  number: 8080
EOF

  wait_for_pods
  info "=== MASTER DONE ==="
  info "Chạy worker1/2/3 sau khi có K3S_TOKEN và MASTER_IP bên trên"
}

# ─── Worker ─────────────────────────────────────────────────────────────────

setup_worker() {
  system_prepare

  [ -z "$MASTER_IP" ] && error "MASTER_IP chưa set"
  [ -z "$K3S_TOKEN" ] && error "K3S_TOKEN chưa set"

  info "Cài k3s worker (role=$VPS_ROLE)..."
  curl -sfL https://get.k3s.io | \
    K3S_URL="https://$MASTER_IP:6443" \
    K3S_TOKEN="$K3S_TOKEN" \
    sh -s - --node-name "$VPS_ROLE"

  mkdir -p ~/.kube
  sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config 2>/dev/null || true
  sudo chown $(id -u):$(id -g) ~/.kube/config 2>/dev/null || true

  # Label node
  case "$VPS_ROLE" in
    worker1)
      kubectl label node worker1 node-role.kubernetes.io/worker=true --overwrite || true
      kubectl label node worker1 dedicated=redis --overwrite || true
      ;;
    worker2)
      kubectl label node worker2 node-role.kubernetes.io/worker=true --overwrite || true
      kubectl label node worker2 dedicated=clickhouse --overwrite || true
      ;;
    worker3)
      kubectl label node worker3 node-role.kubernetes.io/worker=true --overwrite || true
      kubectl label node worker3 dedicated=prometheus --overwrite || true
      ;;
  esac

  info "=== WORKER $VPS_ROLE DONE ==="
}

# ─── Main ───────────────────────────────────────────────────────────────────

case "$VPS_ROLE" in
  master)   setup_master ;;
  worker1)  setup_worker ;;
  worker2)  setup_worker ;;
  worker3)  setup_worker ;;
  *)        usage ;;
esac
