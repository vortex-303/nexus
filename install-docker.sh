#!/bin/bash
set -e

echo "============================================"
echo "  Nexus — Self-Hosted AI Workspace (Docker)"
echo "============================================"
echo ""

# Check for Docker
if ! command -v docker &> /dev/null; then
    echo "Docker not found. Installing..."
    if command -v apt-get &> /dev/null; then
        curl -fsSL https://get.docker.com | sh
    elif command -v apk &> /dev/null; then
        apk add --no-cache docker docker-compose
        rc-update add docker default
        service docker start
    else
        echo "Please install Docker manually: https://docs.docker.com/engine/install/"
        exit 1
    fi
fi

# Check for docker compose
if ! docker compose version &> /dev/null; then
    echo "Docker Compose not found. Please install Docker Compose v2."
    exit 1
fi

# Create install directory
INSTALL_DIR="/opt/nexus"
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

# Ask for domain (optional)
echo ""
read -p "Domain name for SSL (leave blank for HTTP-only): " DOMAIN

if [ -n "$DOMAIN" ]; then
    # Production setup with Caddy SSL
    cat > docker-compose.yml <<'COMPOSE'
services:
  nexus:
    image: ghcr.io/vortex-303/nexus:latest
    volumes:
      - nexus_data:/data
    environment:
      DATA_DIR: /data
    restart: unless-stopped
  caddy:
    image: caddy:2-alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - caddy_data:/data
      - ./Caddyfile:/etc/caddy/Caddyfile
    restart: unless-stopped
volumes:
  nexus_data:
  caddy_data:
COMPOSE

    cat > Caddyfile <<EOF
$DOMAIN {
    reverse_proxy nexus:8080
}
EOF
    echo ""
    echo "SSL configured for $DOMAIN"
else
    # Simple HTTP setup
    cat > docker-compose.yml <<'COMPOSE'
services:
  nexus:
    image: ghcr.io/vortex-303/nexus:latest
    ports:
      - "80:8080"
    volumes:
      - nexus_data:/data
    environment:
      DATA_DIR: /data
    restart: unless-stopped
volumes:
  nexus_data:
COMPOSE
fi

# Pull and start
echo ""
echo "Starting Nexus..."
docker compose up -d

echo ""
echo "============================================"
if [ -n "$DOMAIN" ]; then
    echo "  Nexus is running at https://$DOMAIN"
else
    IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "localhost")
    echo "  Nexus is running at http://$IP"
fi
echo ""
echo "  Data stored in: $INSTALL_DIR"
echo "  Manage: cd $INSTALL_DIR && docker compose [up|down|logs]"
echo "============================================"
