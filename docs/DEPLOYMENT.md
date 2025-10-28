# ZipItJit Deployment Guide

## What Changed

### 1. Temp File Location (IMPORTANT!)
**Changed from `/tmp` to `./temp` in working directory**

**Why?**
- `/tmp` is often `tmpfs` (RAM-backed) on modern systems
- 2GB files would exhaust RAM and crash the system
- Using disk-backed storage in cwd is safer

**What happens:**
- Creates `./temp` directory at startup
- All downloads go to `./temp/download_*`
- Automatic cleanup every 10 minutes removes files older than 1 hour
- Jobs older than 30 minutes are cleaned up

### 2. Security Hardening via Systemd

The service now runs with:
- **Dedicated user**: `zipitjit` (no shell, no login)
- **Isolated `/tmp`**: Service has its own private tmp
- **Read-only filesystem**: Can only write to `/opt/zipitjit/temp`
- **Network restrictions**: Only IPv4/IPv6 allowed
- **No privilege escalation**: Can't gain root
- **Process limits**: Max 64 processes, 1024 file descriptors
- **Kernel protection**: Can't load modules or modify kernel

### 3. Directory Structure

```
/opt/zipitjit/
├── sv_ZIJ           (executable, 0750)
├── cert.pem         (certificate, 0640)
├── key.pem          (private key, 0640)
└── temp/            (temp files, 0750)
    ├── download_*   (temp downloads)
    └── *.zip        (temp zip files)
```

---

## Installation

### Prerequisites

1. You have built the `sv_ZIJ` binary
2. You have `cert.pem` and `key.pem` in the repository root
3. You are in the repository root directory

### Quick Install

```bash
# Make sure you're in the repository root
cd /path/to/ZipItJit

# Run setup script
sudo ./deployment/setup.sh
```

That's it! The script will:
1. Create the `zipitjit` user
2. Set up `/opt/zipitjit` with proper permissions
3. Install the systemd service
4. Start the service

### Manual Install (if you prefer)

```bash
# Create user
sudo useradd -r -s /bin/false -d /opt/zipitjit -m zipitjit

# Create directory structure
sudo mkdir -p /opt/zipitjit/temp

# Copy files
sudo cp sv_ZIJ cert.pem key.pem /opt/zipitjit/

# Set permissions
sudo chown -R zipitjit:zipitjit /opt/zipitjit
sudo chmod 750 /opt/zipitjit
sudo chmod 750 /opt/zipitjit/temp
sudo chmod 640 /opt/zipitjit/{cert.pem,key.pem}
sudo chmod 750 /opt/zipitjit/sv_ZIJ

# Install service
sudo cp deployment/zipitjit.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable zipitjit
sudo systemctl start zipitjit
```

---

## Service Management

### Check Status
```bash
sudo systemctl status zipitjit
```

### View Logs
```bash
# Follow logs in real-time
sudo journalctl -u zipitjit -f

# View last 100 lines
sudo journalctl -u zipitjit -n 100

# View logs since boot
sudo journalctl -u zipitjit -b
```

### Restart Service
```bash
sudo systemctl restart zipitjit
```

### Stop Service
```bash
sudo systemctl stop zipitjit
```

### Disable Service (won't start on boot)
```bash
sudo systemctl disable zipitjit
```

---

## Updating the Service

If you make code changes:

```bash
# Build new binary
go build -o sv_ZIJ sv_ZIJ.go

# Copy to production
sudo cp sv_ZIJ /opt/zipitjit/
sudo chown zipitjit:zipitjit /opt/zipitjit/sv_ZIJ
sudo chmod 750 /opt/zipitjit/sv_ZIJ

# Restart
sudo systemctl restart zipitjit
```

---

## Monitoring

### Check Disk Usage (temp files)
```bash
sudo du -sh /opt/zipitjit/temp
sudo ls -lh /opt/zipitjit/temp
```

### Check Running Processes
```bash
sudo ps aux | grep sv_ZIJ
```

### Check Open Files
```bash
sudo lsof -u zipitjit
```

---

## Security Notes

1. **Service runs as `zipitjit` user** - can't access other users' files
2. **Can only write to `/opt/zipitjit/temp`** - rest of filesystem is read-only
3. **No shell access** - even if compromised, attacker has no shell
4. **Network isolation** - only IPv4/IPv6, no Unix sockets
5. **Private /tmp** - service can't see other processes' temp files
6. **Automatic cleanup** - old files removed every 10 minutes

---

## Troubleshooting

### Service won't start
```bash
# Check logs
sudo journalctl -u zipitjit -n 50

# Check permissions
sudo ls -la /opt/zipitjit

# Check if binary works
sudo -u zipitjit /opt/zipitjit/sv_ZIJ
```

### Permission denied errors
```bash
# Reset permissions
sudo chown -R zipitjit:zipitjit /opt/zipitjit
sudo chmod 750 /opt/zipitjit
sudo chmod 750 /opt/zipitjit/temp
```

### Disk filling up
```bash
# Check temp directory size
sudo du -sh /opt/zipitjit/temp

# Manually clean up (service does this automatically)
sudo rm /opt/zipitjit/temp/download_*
sudo rm /opt/zipitjit/temp/*.zip
```

---

## Uninstall

```bash
# Stop and disable service
sudo systemctl stop zipitjit
sudo systemctl disable zipitjit
sudo rm /etc/systemd/system/zipitjit.service
sudo systemctl daemon-reload

# Remove files
sudo rm -rf /opt/zipitjit

# Remove user
sudo userdel zipitjit
```

---

## Access

The service runs on: **https://0.0.0.0:9443/ZIJ**
