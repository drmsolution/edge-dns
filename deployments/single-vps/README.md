# Triển khai Edge DNS trên 1 VPS

## Cách dùng

```bash
ssh root@<IP_VPS>

export DOMAIN=dns.example.com
export EMAIL=admin@example.com

curl -fsSL https://raw.githubusercontent.com/drmsolution/edge-dns/main/deployments/single-vps/deploy.sh | sudo -E bash
```

Hoặc clone về chạy thủ công:

```bash
git clone https://github.com/drmsolution/edge-dns.git /opt/edge-dns
cd /opt/edge-dns

export DOMAIN=dns.example.com
export EMAIL=admin@example.com
sudo -E bash deployments/single-vps/deploy.sh
```

## Kiến trúc

```
VPS (1 máy)
┌──────────────────────────────────────┐
│  docker-compose.yml                  │
│  ┌──────┐  ┌──────────┐  ┌────────┐ │
│  │redis │  │clickhouse│  │edge-dns│ │
│  └──────┘  └──────────┘  └───┬────┘ │
│                         ┌────┴────┐  │
│                         │admin-api│  │
│                         └─────────┘  │
│                         ┌──────────┐ │
│                         │sniproxy  │ │
│                         └──────────┘ │
└──────────────────────────────────────┘
```

- **edge-dns**: DNS server (UDP/TCP 8053, DoH 8443, DoT 8853)
- **admin-api**: REST API quản lý rules (8080)
- **sniproxy**: SNI proxy chặn/redirect (443)
- **redis**: Cache + session
- **clickhouse**: DNS query logs

## Yêu cầu

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| CPU | 1 core | 2 cores |
| RAM | 1 GB | 2 GB |
| Disk | 20 GB | 50 GB |
| OS | Ubuntu 22.04+ | Ubuntu 24.04 |

## Cổng cần mở trên firewall VPS

| Port | Protocol | Service |
|------|----------|---------|
| 22 | TCP | SSH |
| 8053 | TCP+UDP | DNS query |
| 8443 | TCP | DNS-over-HTTPS |
| 8853 | TCP | DNS-over-TLS |
| 443 | TCP | SNI Proxy |
| 8080 | TCP | Admin API |
| 2112 | TCP | Prometheus metrics |

## Cập nhật

```bash
cd /opt/edge-dns
git pull
docker compose build
docker compose up -d
```

## Logs

```bash
cd /opt/edge-dns
docker compose logs -f
docker compose logs -f edge-dns
```
