# Example Files

## Generating Self-Signed Certificates

For development/testing, you can generate self-signed certificates:

```bash
# Generate self-signed certificate (valid for 365 days)
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes \
  -subj "/C=US/ST=State/L=City/O=Organization/CN=localhost"
```

This will create:
- `cert.pem` - The certificate
- `key.pem` - The private key

## For Production

**DO NOT use self-signed certificates in production!**

Use proper certificates from:
- Let's Encrypt (free, automated)
- Your domain registrar
- A commercial CA

## Cloudflare Tunnel Setup

If using Cloudflare tunnel (as in this project), the tunnel handles TLS termination. You can:

1. Use self-signed certs (Cloudflare tunnel connects to backend over HTTPS but doesn't verify)
2. Configure Cloudflare tunnel with `no-tls-verify` option
3. Use proper certificates if you prefer

The certificates are needed for the HTTPS server but Cloudflare handles public-facing TLS.
