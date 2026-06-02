# Triển khai Edge DNS lên 4 VPS Ubuntu Production

## Kiến trúc

```
Internet ←── DNS queries (UDP/TCP 8053, DoH 8443, DoT 8853)
                │
    ┌───────────┴───────────┐
    │   LoadBalancer/NodePort │
    │   (VPS 1-4:3053/30443)  │
    └───────────┬───────────┘
                │
    ┌───────────┴──────────────────────────────────────┐
    │              Kubernetes (K3s) Cluster            │
    │                                                   │
    │  VPS 1 (Master)     VPS 2 (Worker)   VPS 3 (Worker)  VPS 4 (Worker) │
    │  ┌──────────────┐  ┌─────────────┐  ┌──────────────┐  ┌───────────┐ │
    │  │ cert-manager  │  │  edge-dns   │  │  edge-dns    │  │  edge-dns  │ │
    │  │ nginx-ingress │  │  redis      │  │  clickhouse  │  │ prometheus │ │
    │  │ admin-api     │  │             │  │              │  │           │ │
    │  │ grafana       │  │             │  │              │  │           │ │
    │  └──────────────┘  └─────────────┘  └──────────────┘  └───────────┘ │
    └───────────────────────────────────────────────────────────────────────┘
```

| VPS | RAM tối thiểu | Role | Services |
|-----|--------------|------|----------|
| VPS 1 | 2GB | Master | k3s control-plane, cert-manager, nginx-ingress, admin-api, grafana |
| VPS 2 | 1GB | Worker 1 | edge-dns (replica), redis |
| VPS 3 | 2GB | Worker 2 | edge-dns (replica), clickhouse |
| VPS 4 | 1GB | Worker 3 | edge-dns (replica), prometheus |

---

## Bước 1: Chuẩn bị

### 1.1 Yêu cầu

- **4 VPS Ubuntu 22.04+**, mỗi VPS có IP public riêng
- **1 domain** (ví dụ: `dns.example.com`) — trỏ A record đến IP các VPS
- **Cổng mở**: 80, 443, 6443 (k3s), 8053 (DNS), 8443 (DoH), 8853 (DoT)

### 1.2 DNS Records

Tạo tại nơi quản lý domain (Cloudflare, DigitalOcean, v.v.):

| Type | Name | Value | Mục đích |
|------|------|-------|----------|
| A | `dns` | IP VPS 1 | DoH endpoint |
| A | `dns` | IP VPS 2 | DNS query |
| A | `dns` | IP VPS 3 | DNS query |
| A | `dns` | IP VPS 4 | DNS query |
| A | `api.dns` | IP VPS 1 | Admin API |
| A | `grafana.dns` | IP VPS 1 | Grafana UI |

Sau đó đợi 5-10 phút cho DNS propagate.

---

## Bước 2: Cài đặt

### 2.1 Trên VPS 1 — Master Node

SSH vào VPS 1:

```bash
# 1. SSH
ssh root@<IP_VPS_1>

# 2. Clone repo
apt update && apt install -y git
git clone https://github.com/drmsolution/edge-dns.git /opt/edge-dns
cd /opt/edge-dns

# 3. Set biến môi trường
export MASTER_IP="<IP_VPS_1>"
export DOMAIN="dns.example.com"
export EMAIL="admin@example.com"

# 4. Chạy script master
chmod +x deployments/production/setup.sh
./deployments/production/setup.sh master
```

Script sẽ tự động:
- Cài Docker, k3s, Helm
- Tạo K3S_TOKEN (ghi lại!)
- Cài cert-manager + ClusterIssuer Let's Encrypt
- Cài nginx-ingress (NodePort 30080/30443)
- Deploy edge-dns + monitoring
- Tạo Ingress cho DoH, Admin API, Grafana

### 2.2 Trên VPS 2-4 — Worker Nodes

Mở terminal riêng cho mỗi VPS:

**VPS 2 (Worker 1)**:
```bash
ssh root@<IP_VPS_2>
export MASTER_IP="<IP_VPS_1>"
export K3S_TOKEN="<token_tu_master>"

git clone https://github.com/drmsolution/edge-dns.git /opt/edge-dns
cd /opt/edge-dns
./deployments/production/setup.sh worker1
```

**VPS 3 (Worker 2)**:
```bash
ssh root@<IP_VPS_3>
export MASTER_IP="<IP_VPS_1>"
export K3S_TOKEN="<token_tu_master>"

git clone https://github.com/drmsolution/edge-dns.git /opt/edge-dns
cd /opt/edge-dns
./deployments/production/setup.sh worker2
```

**VPS 4 (Worker 3)**:
```bash
ssh root@<IP_VPS_4>
export MASTER_IP="<IP_VPS_1>"
export K3S_TOKEN="<token_tu_master>"

git clone https://github.com/drmsolution/edge-dns.git /opt/edge-dns
cd /opt/edge-dns
./deployments/production/setup.sh worker3
```

---

## Bước 3: Kiểm tra

### 3.1 Trên master — Verify cluster

```bash
kubectl get nodes
kubectl -n edge-dns get pods -o wide
kubectl -n edge-dns get svc
kubectl -n edge-dns get ingress
```

Kết quả mong đợi:

```
NAME     STATUS   ROLES
master   Ready    control-plane
worker1  Ready    worker
worker2  Ready    worker
worker3  Ready    worker

NAME                         READY   STATUS    RESTARTS   IP
edge-dns-xxxxx               1/1     Running   0          10.42.1.x
edge-dns-yyyyy               1/1     Running   0          10.42.2.x
edge-dns-zzzzz               1/1     Running   0          10.42.3.x
redis-xxxxx                  1/1     Running   0          10.42.1.y
clickhouse-xxxxx             1/1     Running   0          10.42.2.y
admin-api-xxxxx              1/1     Running   0          10.42.0.z
prometheus-xxxxx             1/1     Running   0          10.42.3.y
grafana-xxxxx                1/1     Running   0          10.42.0.w
```

### 3.2 Test DNS query

```bash
# Từ VPS master hoặc bất kỳ máy nào:
dig @<IP_VPS_2> -p 8053 google.com
dig @<IP_VPS_3> -p 8053 google.com
dig @<IP_VPS_4> -p 8053 google.com

# Test qua NodePort (nếu firewall chặn 8053):
dig @<IP_VPS_1> -p 30053 google.com
```

### 3.3 Test DoH (DNS over HTTPS)

```bash
# Đợi SSL certificate được issue (2-5 phút)
kubectl -n edge-dns get certificate
kubectl -n edge-dns describe certificate edge-dns-tls

# Khi certificate READY=True:
curl -s "https://dns.example.com/dns-query?name=google.com&type=A" \
  -H "Accept: application/dns-json"
```

### 3.4 Test DoT (DNS over TLS)

Sử dụng `knot-dns-utils` hoặc `dnstap`:

```bash
# Trên VPS master:
apt install -y knot-dns-utils

kdig @dns.example.com -p 8853 +tls google.com
```

### 3.5 Test Admin API

```bash
curl -s https://api.dns.example.com/api/v1/analytics/summary
curl -s https://api.dns.example.com/api/v1/rules | jq .
```

### 3.6 Test Grafana

```bash
# Truy cập:
# https://grafana.dns.example.com
# User: admin / Password: admin
```

Vô **Dashboards → Edge DNS** xem 6 panel metrics.

---

## Bước 4: Phát triển & Update

### 4.1 Dev workflow trên máy local

```bash
# 1. Sửa code
# 2. Test local
go build ./... && go test ./... && go vet ./...

# 3. Commit + push → CI/CD tự build Docker image mới
git add -A
git commit -m "fix: ..."
git push
```

### 4.2 Deploy bản update lên cluster

```bash
# Trên VPS master:
cd /opt/edge-dns
git pull

# Nếu chỉ update image (không đổi cấu hình):
kubectl -n edge-dns rollout restart deployment edge-dns

# Nếu có thay đổi Helm chart:
helm upgrade edge-dns deployments/helm/edge-dns/ \
  --namespace edge-dns \
  --set global.imageTag=latest \
  --reuse-values

# Theo dõi rollout:
kubectl -n edge-dns rollout status deployment edge-dns
```

### 4.3 Xem logs

```bash
kubectl -n edge-dns logs -l app.kubernetes.io/name=edge-dns --tail=50 -f
kubectl -n edge-dns logs -l app.kubernetes.io/name=admin-api --tail=50 -f
kubectl -n edge-dns logs -l app.kubernetes.io/name=clickhouse --tail=50
kubectl -n edge-dns logs -l app.kubernetes.io/name=redis --tail=50
```

---

## Bước 5: Mở rộng

### Thêm worker node

```bash
# Trên VPS mới:
export MASTER_IP="<IP_VPS_1>"
export K3S_TOKEN="<token>"
export VPS_ROLE="worker4"

# Chạy
curl -sfL https://get.k3s.io | K3S_URL="https://$MASTER_IP:6443" K3S_TOKEN="$K3S_TOKEN" sh -
kubectl label node worker4 node-role.kubernetes.io/worker=true

# Scale edge-dns:
kubectl -n edge-dns scale deployment edge-dns --replicas=4
```

### Tăng storage ClickHouse

```bash
# Sửa values.yaml → clickhouse.persistence.data.size = 100Gi
# Hoặc trực tiếp:
kubectl -n edge-dns patch pvc clickhouse-data -p '{"spec":{"resources":{"requests":{"storage":"100Gi"}}}}'
```

---

## Bước 6: Backup & Recovery

### Backup etcd (quan trọng!)

```bash
# Trên master - backup
sudo k3s etcd-snapshot save --name edge-dns-backup
sudo ls /var/lib/rancher/k3s/server/db/snapshots/

# Restore nếu cần
sudo k3s server \
  --cluster-reset \
  --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/edge-dns-backup-<date>
```

### Backup ClickHouse data

```bash
# Backup database dns_logs ra file
kubectl -n edge-dns exec deploy/clickhouse -- clickhouse-client \
  --query "SELECT * FROM dns_logs FORMAT TSV" > /tmp/dns_logs_backup.tsv

# Restore
cat /tmp/dns_logs_backup.tsv | kubectl -n edge-dns exec -i deploy/clickhouse -- \
  clickhouse-client --query "INSERT INTO dns_logs FORMAT TSV"
```

---

## Troubleshooting

| Vấn đề | Nguyên nhân | Fix |
|--------|-------------|-----|
| Pod CrashLoopBackOff | Thiếu Redis/ClickHouse | Kiểm tra `kubectl logs pod/...` |
| DNS query timeout | Firewall chặn port | Mở port: `ufw allow 8053,8443,8853` |
| Certificate not ready | DNS chưa propagate | Đợi 5-10p, check `kubectl describe certificate` |
| ImagePullBackOff | ghcr.io image private | `gh api ... -f visibility=public` hoặc tạo imagePullSecret |
| Worker NotReady | K3S_TOKEN sai | Chạy lại worker với token đúng |

---

## Scripts tham khảo

| File | Mô tả |
|------|-------|
| `deployments/production/setup.sh` | Script cài đặt tự động cho master & worker |
| `deployments/k8s/` | K8s manifests standalone (không Helm) |
| `deployments/helm/edge-dns/` | Helm chart đầy đủ |
| `.github/workflows/ci-cd.yml` | CI/CD pipeline tự động build + push image |

---

## Kiến trúc network

```
Client
  │
  ├── UDP/TCP :8053 ──► NodePort 30053 ──► edge-dns Pod ──► 1.1.1.1:53
  │                                                  │
  ├── DoH    :8443 ──► Ingress ──► edge-dns Pod ─────┤
  │                                      │           │
  ├── DoT    :8853 ──► NodePort 30853 ───┤           │
  │                                      │           │
  │                         ┌────────────┴─────┐     │
  │                         │   Redis (cache)  │     │
  │                         │   ClickHouse(log)│     │
  │                         └──────────────────┘     │
  │                                                  │
  ├── HTTPS  :443  ──► Ingress ──► admin-api:8080
  └── HTTPS  :443  ──► Ingress ──► grafana:3000
```
