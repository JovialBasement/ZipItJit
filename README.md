# ZIP IT JIT! 🔥

**GIGA FILE ENCRYPTION MECHANISM** - A web service for double-encrypting files via URL download, perfect for securely transporting malware samples to analysis environments.

```
🔐🗿 AES-256 DOUBLE ZIP AND ENCRYPTED BRUH 🗿🔐
🇰🇵🚀 MILITARY GRADE ENCRYPTION 🇺🇸🚀
⚡ QUANTUM ENTANGLED PASSWORD SPAWNING (password is 'password') ⚡
🧠 ZERO KNOWLEDGE REQUIRED 🧠
```

---

## What It Does

ZipItJit is a web service that:

1. Takes a URL to a file
2. Downloads it securely (with SSRF protection)
3. Creates a password-protected ZIP of the file
4. Zips that ZIP again (double encryption)
5. Serves it to you with the MD5 hash

**Use case**: Safely download potentially malicious files for transport to isolated analysis environments. The double ZIP with password protection allows secure transfer past security tools that might block direct malware downloads.

**Password**: Both ZIP layers use the password `"password"` (keeping it simple for malware analysis workflows).

---

## Features

### Security
- ✅ **SSRF Protection**: Blocks private IPs, cloud metadata endpoints, DNS rebinding
- ✅ **Input Validation**: URL validation, filename sanitization, file size limits
- ✅ **Rate Limiting**: Prevents abuse (1 req/sec globally)
- ✅ **Server Timeouts**: Protection against slowloris attacks
- ✅ **Sandboxed Execution**: Runs as dedicated low-privilege user
- ✅ **Read-only Filesystem**: Can only write to temp directory

### Features
- 📊 **Real-time Progress**: Live download progress with 100ms polling
- 🔐 **Double ZIP Encryption**: AES-256 password-protected (twice!)
- 🧮 **MD5 Hashing**: Displays MD5 of original downloaded file
- 🧹 **Auto Cleanup**: Removes old jobs and temp files automatically
- 🎨 **Themed UI**: Brain rot hacker aesthetic with Matrix effects
- 📦 **2GB File Limit**: Handles large malware samples

---

## Quick Start

### Prerequisites

- Go 1.21+
- SSL certificate and key (`cert.pem`, `key.pem`)
- Linux with systemd (for production deployment)

### Generate SSL Certificates

```bash
# Self-signed (development/testing)
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes \
  -subj "/C=US/ST=State/L=City/O=Organization/CN=localhost"
```

See [`examples/README.md`](examples/README.md) for more details.

### Build

```bash
go build -o sv_ZIJ sv_ZIJ.go
```

### Run (Development)

```bash
./sv_ZIJ
```

Access at: `https://localhost:9443/ZIJ`

### Deploy (Production)

```bash
sudo ./deployment/setup.sh
```

See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) for full deployment guide.

---

## Usage

1. **Navigate** to `https://your-domain.com/ZIJ`
2. **Paste** the URL of the file you want to download
3. **Click** "ENCRYPT MY RESEARCH"
4. **Watch** real-time download progress
5. **Download** auto-starts when complete
6. **View** MD5 hash of original file
7. **Click** "🔥 ANOTHA ONE 🔥" to download another

---

## Architecture

### Tech Stack
- **Backend**: Go 1.21+ with standard library
- **Security**: Custom SSRF protection, input validation
- **Frontend**: Vanilla JavaScript, no frameworks
- **Styling**: Custom CSS with Matrix/hacker theme
- **Deployment**: Systemd service with hardening

### Flow

```
User submits URL
    ↓
Server validates URL (SSRF protection)
    ↓
Job created with unique ID
    ↓
Background goroutine downloads file
    ↓
Real-time progress via polling (100ms)
    ↓
MD5 calculated during download
    ↓
File double-zipped with password "password"
    ↓
Auto-download to user
    ↓
Cleanup after 30 minutes
```

### Security Layers

1. **URL Validation**: DNS resolution with IP blocklist
2. **Connection-time Validation**: Re-checks IPs at TCP dial (prevents DNS rebinding)
3. **Redirect Protection**: Validates each redirect (max 3)
4. **File Size Limits**: 2GB hard limit
5. **Rate Limiting**: 1 request/second globally
6. **Sandboxing**: Runs as `zipitjit` user with restricted filesystem access

---

## File Structure

```
ZipItJit/
├── README.md                    # This file
├── .gitignore                   # Git ignore rules
├── go.mod                       # Go dependencies
├── go.sum                       # Dependency checksums
├── sv_ZIJ.go                    # Main source code
├── docs/
│   └── DEPLOYMENT.md           # Production deployment guide
├── deployment/
│   ├── zipitjit.service        # Systemd service file
│   └── setup.sh                # Automated setup script
└── examples/
    └── README.md               # Certificate generation guide
```

---

## Configuration

### Environment

The service runs on port **9443** by default. To change:

Edit `sv_ZIJ.go` line ~169:
```go
Addr: "0.0.0.0:9443",  // Change port here
```

### File Size Limit

Default: 2GB. To change, edit `sv_ZIJ.go` line ~27:
```go
maxFileSize = 2000 * 1024 * 1024  // Change here
```

### Cleanup Timers

- **Job cleanup**: 30 minutes (line ~181)
- **Temp file cleanup**: 1 hour (line ~208)
- **Cleanup interval**: 10 minutes (line ~160)

---

## API Endpoints

- `GET /ZIJ` - Serve main UI
- `POST /ZIJ` - Create new download job (returns `{"job_id": "uuid"}`)
- `GET /ZIJ/progress/{jobID}` - Get job progress (JSON)
- `GET /ZIJ/download/{jobID}` - Download completed ZIP file

---

## Dependencies

```go
github.com/alexmullins/zip     // Password-protected ZIP creation
github.com/google/uuid         // Job ID generation
golang.org/x/time/rate         // Rate limiting
```

Install:
```bash
go mod download
```

---

## Security Considerations

### SSRF Protection

The service implements comprehensive SSRF protection:

- Blocks private IP ranges (10.0.0.0/8, 192.168.0.0/16, 172.16.0.0/12)
- Blocks loopback (127.0.0.0/8, ::1)
- Blocks link-local (169.254.0.0/16 - cloud metadata)
- Blocks IPv6 private ranges (fc00::/7, fe80::/10)
- Validates IPs at DNS resolution AND TCP dial time (prevents DNS rebinding)
- Validates all redirects with fresh DNS resolution

### Known Limitations

- **Rate limiting is global**: One user can lock out others (1 req/sec total)
  - Consider implementing per-IP rate limiting for production
- **Password is hardcoded**: Not meant for actual security, just malware transport
- **No authentication**: Anyone can use the service
  - Consider adding API keys or IP allowlists for production

---

## Use Cases

- **Malware Analysis**: Download malware samples safely for sandbox analysis
- **Security Research**: Transport potentially harmful files to isolated environments
- **CTF Challenges**: Safely distribute challenge files
- **Pentesting**: Download tools and payloads to test environments

---

## Legal Notice

⚠️ **This tool is designed for security research and malware analysis.**

- Only download files you have legal authorization to access
- Only transport files to environments you control
- Malware samples should only be analyzed in isolated environments
- Be aware of laws regarding malware possession in your jurisdiction

---

## Contributing

Contributions welcome! This project is intentionally over-the-top in its presentation while being serious about security.

When contributing:
- Maintain the brain rot aesthetic in UI text
- Keep security features robust and well-tested
- Add tests for any new SSRF bypass techniques
- Update documentation

---

## License

MIT License - See LICENSE file for details

---

## Credits

Built with:
- Maximum brain rot energy 🧠
- Ironic security theater 🎭
- Actual security engineering 🔒
- Go standard library excellence 📚

**NO CAP THIS IS BUSSIN FR FR** 🔥

---

## Support

- 📖 Documentation: See `docs/` folder
- 🐛 Issues: Open an issue on GitHub
- 💬 Questions: Start a discussion

---

*"MILITARY GRADE ENCRYPTION" (password is literally "password")*
