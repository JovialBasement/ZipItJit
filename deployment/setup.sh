#!/bin/bash
set -e

echo "=========================================="
echo "  ZipItJit - GIGA FILE ENCRYPTION SETUP  "
echo "=========================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo "ERROR: Please run as root (use sudo)"
    exit 1
fi

# Check required files exist
if [ ! -f "sv_ZIJ" ]; then
    echo "ERROR: sv_ZIJ binary not found in current directory"
    exit 1
fi

if [ ! -f "cert.pem" ] || [ ! -f "key.pem" ]; then
    echo "ERROR: cert.pem or key.pem not found in current directory"
    exit 1
fi

if [ ! -f "deployment/zipitjit.service" ]; then
    echo "ERROR: deployment/zipitjit.service file not found"
    echo "Make sure you're running this from the repository root"
    exit 1
fi

echo "[1/7] Creating zipitjit user..."
if id "zipitjit" &>/dev/null; then
    echo "  → User already exists"
else
    useradd -r -s /bin/false -d /opt/zipitjit -m zipitjit
    echo "  → User created"
fi

echo "[2/7] Creating directory structure..."
mkdir -p /opt/zipitjit/temp
echo "  → Directories created"

echo "[3/7] Copying files..."
cp sv_ZIJ /opt/zipitjit/
cp cert.pem /opt/zipitjit/
cp key.pem /opt/zipitjit/
echo "  → Files copied"

echo "[4/7] Setting permissions..."
chown -R zipitjit:zipitjit /opt/zipitjit
chmod 750 /opt/zipitjit
chmod 750 /opt/zipitjit/temp
chmod 640 /opt/zipitjit/cert.pem
chmod 640 /opt/zipitjit/key.pem
chmod 750 /opt/zipitjit/sv_ZIJ
echo "  → Permissions set"

echo "[5/7] Installing systemd service..."
cp deployment/zipitjit.service /etc/systemd/system/
systemctl daemon-reload
echo "  → Service installed"

echo "[6/7] Enabling service..."
systemctl enable zipitjit
echo "  → Service enabled"

echo "[7/7] Starting service..."
systemctl start zipitjit
echo "  → Service started"

echo ""
echo "=========================================="
echo "  SETUP COMPLETE!                        "
echo "=========================================="
echo ""
echo "Service status:"
systemctl status zipitjit --no-pager
echo ""
echo "Useful commands:"
echo "  sudo systemctl status zipitjit    - Check status"
echo "  sudo systemctl restart zipitjit   - Restart service"
echo "  sudo systemctl stop zipitjit      - Stop service"
echo "  sudo journalctl -u zipitjit -f    - View logs"
echo ""
echo "Service is running on: https://0.0.0.0:9443/ZIJ"
echo ""
