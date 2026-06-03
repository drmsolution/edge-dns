Hướng dẫn triển khai Edge DNS trên 1 VPS

▸ Yêu cầu: Ubuntu 22.04+, 1 CPU, 1GB RAM, 20GB disk
▸ Thời gian: ~15 phút (tuỳ tốc độ mạng)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
1. CHUẨN BỊ VPS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Mở các cổng trên firewall VPS:

  22/tcp   SSH
  8053     DNS (TCP+UDP)
  8443/tcp DNS-over-HTTPS
  8853/tcp DNS-over-TLS
  443/tcp  SNI Proxy
  8080/tcp Admin API
  2112/tcp Prometheus metrics

Nếu dùng DigitalOcean / Vultr / Hetzner: vào web panel mở
các cổng trên trong phần Firewall / Security Group.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
2. SSH VÀO VPS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  ssh root@<ĐỊA_CHỈ_IP_VPS>

Nếu dùng user không phải root, nhớ thêm sudo cho user đó.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
3. CÀI ĐẶT (tự động)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Chạy 3 lệnh sau:

  apt update && apt install -y git curl
  git clone https://github.com/drmsolution/edge-dns.git /opt/edge-dns
  cd /opt/edge-dns

  # Set domain (nếu có) và email cho Let's Encrypt
  export DOMAIN=dns.espeech.us
  export EMAIL=admin@espeech.us

  # Chạy script deploy
  sudo -E bash deployments/single-vps/deploy.sh

Script tự động làm tất cả:
  • Cập nhật hệ thống
  • Cài Docker + Docker Compose
  • Clone code mới nhất
  • Build Docker images
  • Cấu hình firewall (ufw)
  • Tạo file .env với ADMIN_API_KEY ngẫu nhiên
  • Chạy 5 containers: redis, clickhouse, edge-dns,
    admin-api, sniproxy

Kết thúc script sẽ in ra ADMIN_API_KEY — hãy copy lại.

Nếu lỡ mất, xem lại bằng lệnh:

  grep ADMIN_API_KEY /opt/edge-dns/.env

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
4. KIỂM TRA
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

4.1. Health check

  curl http://127.0.0.1:2112/healthz
  curl http://127.0.0.1:2112/readyz

Kết quả mong đợi: {"status":"ok"}

4.2. DNS query thử

  dig @127.0.0.1 -p 8053 google.com
  dig @127.0.0.1 -p 8053 google.com AAAA

Phải trả về địa chỉ IP thật (không phải 0.0.0.0).

Test domain bị chặn (có trong fallback list):

  dig @127.0.0.1 -p 8053 doubleclick.net

Phải trả về 0.0.0.0 (NXDOMAIN).

4.3. Admin API

  KEY=$(grep ADMIN_API_KEY /opt/edge-dns/.env | cut -d= -f2)
  curl -H "Authorization: Bearer $KEY" \
    http://127.0.0.1:8080/api/v1/rules?user_id=test

Kết quả: {"user_id":"test","domains":[]}

  # Thêm rule chặn
  curl -H "Authorization: Bearer $KEY" \
    -X POST http://127.0.0.1:8080/api/v1/rules \
    -d '{"user_id":"test","domain":"facebook.com","action":"block"}'

  # Kiểm tra lại
  dig @127.0.0.1 -p 8053 facebook.com
  # → trả về 0.0.0.0

  # Thêm redirect (chuyển hướng netflix.com về IP khác)
  curl -H "Authorization: Bearer $KEY" \
    -X POST http://127.0.0.1:8080/api/v1/redirects \
    -d '{"user_id":"test","domain":"netflix.com","target_ip":"10.0.0.1"}'

4.4. Xem logs

  cd /opt/edge-dns
  docker compose logs -f          # tất cả services
  docker compose logs -f edge-dns # chỉ edge-dns
  docker compose logs -f admin-api

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
5. DNS TỪ XA (từ máy tính của bạn)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Dùng IP public của VPS:

  dig @<IP_VPS> -p 8053 google.com

Nếu không dig được → kiểm tra firewall VPS đã mở port 8053.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
6. SSL CHO DoH (nếu có domain)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Trỏ DNS trước:

  A record   dns.example.com → <IP_VPS>
  A record   api.dns.example.com → <IP_VPS>

Đợi 5-10 phút cho DNS propagate, rồi chạy:

  cd /opt/edge-dns
  sudo bash deployments/single-vps/deploy.sh --ssl

Script sẽ:
  • Cài Nginx + certbot
  • Get Let's Encrypt SSL cert
  • Cấu hình reverse proxy cho DoH + Admin API
  • Ghi đường dẫn cert vào .env

Sau đó test DoH:

  curl -s "https://dns.example.com/dns-query?name=google.com&type=A" \
    -H "Accept: application/dns-json"

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
7. CẬP NHẬT
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Khi có code mới:

  cd /opt/edge-dns
  git pull
  docker compose build
  docker compose up -d

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
8. XỬ LÝ SỰ CỐ
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Container không chạy:

  docker compose ps
  docker compose logs edge-dns

Redis lỗi:

  docker compose exec redis redis-cli ping
  # Phải trả về: PONG

ClickHouse lỗi:

  docker compose exec clickhouse clickhouse-client --query "SELECT 1"
  # Phải trả về: 1

Không dig được:

  netstat -tulpn | grep 8053
  # Phải thấy edge-dns đang listen

  ufw status
  # Phải thấy 8053/tcp và 8053/udp ALLOW

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
9. THAM KHẢO
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

• Script deploy:   /opt/edge-dns/deployments/single-vps/deploy.sh
• Docker Compose:  /opt/edge-dns/docker-compose.yml
• Cấu hình:        /opt/edge-dns/.env
• Logs:            docker compose logs -f
• Restart:         docker compose restart
• Dừng:            docker compose down
