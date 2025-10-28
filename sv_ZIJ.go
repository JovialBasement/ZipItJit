package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alexmullins/zip"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

const (
	maxFileSize     = 2000 * 1024 * 1024 // 2GB limit
	maxFilenameLen  = 255
	downloadTimeout = 5 * time.Minute
)

var (
	limiter        = rate.NewLimiter(1, 2) // 1 request per second, burst of 5
	allowedSchemes = map[string]bool{"http": true, "https": true}
	filenameRegex  = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// Job represents a download/encryption job with progress tracking
type Job struct {
	ID          string    `json:"id"`
	Progress    int       `json:"progress"`     // 0-100
	StatusText  string    `json:"status_text"`  // "Downloading...", "Zipping...", etc.
	OriginalMD5 string    `json:"original_md5"` // MD5 of downloaded file
	ZipPath     string    `json:"-"`            // Path to final zip file (not sent to client)
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"-"`
}

var (
	jobs      = make(map[string]*Job)
	jobsMutex sync.RWMutex
)

// isBlockedIP checks if an IP address is in a blocked range (private, loopback, link-local, etc.)
func isBlockedIP(ip net.IP) bool {
	// Comprehensive list of blocked IP ranges
	blockedRanges := []string{
		// IPv4 private ranges
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		// IPv4 special-use ranges
		"127.0.0.0/8",    // Loopback
		"169.254.0.0/16", // Link-local (includes cloud metadata)
		"0.0.0.0/8",      // Current network
		"224.0.0.0/4",    // Multicast
		"240.0.0.0/4",    // Reserved
		// IPv6 special ranges
		"fc00::/7",  // Unique Local Addresses
		"fe80::/10", // Link-local
		"::1/128",   // Loopback
		"ff00::/8",  // Multicast
	}

	for _, cidr := range blockedRanges {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// validateAndResolveURL validates a URL and resolves its hostname to ensure no IPs are in blocked ranges
func validateAndResolveURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("malformed URL")
	}

	// Check scheme
	if !allowedSchemes[u.Scheme] {
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	// Extract hostname
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("missing hostname")
	}

	// Resolve hostname to IP addresses
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed")
	}

	// Validate all resolved IPs
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("access to host denied: resolves to blocked IP")
		}
	}

	return u, nil
}

// progressWriter wraps an io.Writer and tracks progress
type progressWriter struct {
	writer      io.Writer
	total       int64
	written     int64
	jobID       string
	statusPrefix string
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	pw.written += int64(n)

	// Update job progress
	if pw.total > 0 {
		percentage := int((float64(pw.written) / float64(pw.total)) * 100)
		if percentage > 100 {
			percentage = 100
		}

		jobsMutex.Lock()
		if job, exists := jobs[pw.jobID]; exists {
			job.Progress = percentage
			job.StatusText = fmt.Sprintf("%s %d%%", pw.statusPrefix, percentage)
		}
		jobsMutex.Unlock()
	}

	return n, err
}

func main() {
	// Create temp directory in current working directory (avoid RAM-backed /tmp)
	if err := os.MkdirAll("./temp", 0750); err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}

	http.HandleFunc("/", handler)

	// Start cleanup goroutine to remove old jobs
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			cleanupOldJobs()
		}
	}()

	// Configure server with timeouts to prevent slowloris and resource exhaustion
	srv := &http.Server{
		Addr:         "0.0.0.0:9443",
		ReadTimeout:  15 * time.Second,  // Time to read request headers and body
		WriteTimeout: 10 * time.Minute,  // Time to write response (needs to be long for large file downloads)
		IdleTimeout:  60 * time.Second,  // Time to keep idle connections open
	}

	log.Printf("Server starting on https://0.0.0.0:9443")
	log.Fatal(srv.ListenAndServeTLS("cert.pem", "key.pem"))
}

func cleanupOldJobs() {
	cutoff := time.Now().Add(-30 * time.Minute)

	jobsMutex.Lock()
	defer jobsMutex.Unlock()

	for id, job := range jobs {
		if job.CreatedAt.Before(cutoff) {
			// Remove zip file if it exists
			if job.ZipPath != "" {
				os.Remove(job.ZipPath)
			}
			delete(jobs, id)
			log.Printf("Cleaned up old job: %s", id)
		}
	}

	// Clean up orphaned temp files older than 1 hour
	cleanupTempFiles()
}

func cleanupTempFiles() {
	entries, err := os.ReadDir("./temp")
	if err != nil {
		log.Printf("Failed to read temp directory: %v", err)
		return
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join("./temp", entry.Name())
			if err := os.Remove(path); err != nil {
				log.Printf("Failed to remove old temp file %s: %v", path, err)
			} else {
				log.Printf("Cleaned up old temp file: %s", entry.Name())
			}
		}
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	// Normalize path - remove trailing slash for comparison
	path := strings.TrimSuffix(r.URL.Path, "/")

	switch r.Method {
	case "GET":
		if path == "/ZIJ" {
			serveForm(w)
		} else if strings.HasPrefix(path, "/ZIJ/progress/") {
			jobID := strings.TrimPrefix(path, "/ZIJ/progress/")
			serveProgress(w, jobID)
		} else if strings.HasPrefix(path, "/ZIJ/download/") {
			jobID := strings.TrimPrefix(path, "/ZIJ/download/")
			serveDownload(w, jobID)
		} else {
			http.NotFound(w, r)
		}
	case "POST":
		if path == "/ZIJ" {
			createJob(w, r)
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func serveForm(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
    <title> ZIP IT JIT </title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Orbitron:wght@400;700;900&display=swap');
        
        * { margin: 0; padding: 0; box-sizing: border-box; }
        
        body {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            font-family: 'Orbitron', monospace;
            overflow: hidden;
            position: relative;
        }
        
        .matrix {
            position: fixed;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            pointer-events: none;
            z-index: 1;
        }
        
        .matrix::before {
            content: '01001001 01001000 01000001 01000011 01001011 01000101 01000100';
            position: absolute;
            top: -100%;
            left: 0;
            width: 100%;
            height: 200%;
            color: rgba(0, 255, 0, 0.1);
            font-size: 12px;
            line-height: 1.2;
            white-space: pre-wrap;
            animation: matrixRain 10s linear infinite;
        }
        
        @keyframes matrixRain {
            0% { transform: translateY(-100%); }
            100% { transform: translateY(100%); }
        }
        
        .container {
            background: rgba(0, 0, 0, 0.9);
            padding: 50px;
            border-radius: 20px;
            border: 3px solid #00ff00;
            box-shadow: 0 0 50px rgba(0, 255, 0, 0.5), inset 0 0 30px rgba(0, 255, 0, 0.1);
            text-align: center;
            max-width: 600px;
            width: 90%;
            position: relative;
            z-index: 10;
            animation: pulse 2s ease-in-out infinite alternate;
        }
        
        @keyframes pulse {
            0% { box-shadow: 0 0 50px rgba(0, 255, 0, 0.5), inset 0 0 30px rgba(0, 255, 0, 0.1); }
            100% { box-shadow: 0 0 80px rgba(0, 255, 0, 0.8), inset 0 0 50px rgba(0, 255, 0, 0.2); }
        }
        
        h1 {
            color: #00ff00;
            font-size: 2.5rem;
            font-weight: 900;
            margin-bottom: 10px;
            text-shadow: 0 0 20px #00ff00;
            animation: textGlow 1.5s ease-in-out infinite alternate;
        }
        
        @keyframes textGlow {
            0% { text-shadow: 0 0 20px #00ff00; }
            100% { text-shadow: 0 0 30px #00ff00, 0 0 40px #00ff41; }
        }
        
        .subtitle {
			color: #00ff00;
			font-size: 1.1rem;
			font-weight: 900;
			margin-bottom: 5px;
			text-shadow: 0 0 20px #00ff00;
			animation: textGlow 1.5s ease-in-out infinite alternate;
        }

		.subtitle-small {
            color: #00ff41;
            font-size: 0.8rem;
            margin-bottom: 25px;
            text-shadow: 0 0 10px #00ff41;
            opacity: 0.9;
        }
        
        .form-group {
            margin: 30px 0;
            position: relative;
        }
        
        input[type="url"] {
            width: 100%;
            padding: 20px;
            font-size: 18px;
            background: rgba(0, 0, 0, 0.8);
            border: 2px solid #00ff00;
            border-radius: 10px;
            color: #00ff00;
            font-family: 'Orbitron', monospace;
            outline: none;
            transition: all 0.3s ease;
        }
        
        input[type="url"]:focus {
            border-color: #ff0080;
            box-shadow: 0 0 20px rgba(255, 0, 128, 0.5);
            transform: scale(1.02);
        }
        
        input[type="url"]::placeholder {
            color: rgba(0, 255, 0, 0.6);
        }
        
        .hack-button {
            background: linear-gradient(45deg, #00ff00, #00ff41, #00ff00);
            border: none;
            padding: 20px 40px;
            font-size: 1rem;
            font-weight: 700;
            color: #000;
            border-radius: 15px;
            cursor: pointer;
            font-family: 'Orbitron', monospace;
            text-transform: uppercase;
            letter-spacing: 2px;
            position: relative;
            overflow: hidden;
            transition: all 0.3s ease;
            margin-top: 20px;
        }
        
        .hack-button:hover {
            transform: scale(1.1);
            box-shadow: 0 0 30px rgba(0, 255, 0, 0.8);
        }
        
        .hack-button:active {
            transform: scale(0.95);
        }
        
        .hack-button::before {
            content: '';
            position: absolute;
            top: 0;
            left: -100%;
            width: 100%;
            height: 100%;
            background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.4), transparent);
            transition: left 0.5s;
        }
        
        .hack-button:hover::before {
            left: 100%;
        }
        
        .warning {
            color: #ff4444;
            font-size: 12px;
            margin-top: 20px;
            animation: blink 1s infinite;
        }
        
        @keyframes blink {
            0%, 50% { opacity: 1; }
            51%, 100% { opacity: 0.3; }
        }
        
        .features {
            margin: 20px 0;
            text-align: center;
        }

        .feature-item {
            color: #00ff00;
            font-size: 0.85rem;
            margin: 8px 0;
            text-shadow: 0 0 8px #00ff00;
            padding: 5px 0;
        }

        .password-subtext {
            font-size: 0.75rem;
            margin-top: 5px;
            opacity: 0.8;
        }

        .disclaimer {
            margin-top: 25px;
            text-align: center;
        }

        .disclaimer-line {
            color: #ff0080;
            font-size: 0.7rem;
            margin: 5px 0;
            text-shadow: 0 0 8px #ff0080;
            opacity: 0.8;
        }
        
        @media (max-width: 768px) {
            h1 { font-size: 1rem; }
            .container { padding: 30px 20px; }
            input[type="url"] { font-size: 16px; }
            .hack-button { padding: 15px 30px; font-size: 1rem; }
        }

        /* Loading Container */
        .loading-container {
            background: rgba(0, 0, 0, 0.9);
            padding: 50px;
            border-radius: 20px;
            border: 3px solid #00ff00;
            box-shadow: 0 0 50px rgba(0, 255, 0, 0.5), inset 0 0 30px rgba(0, 255, 0, 0.1);
            text-align: center;
            max-width: 600px;
            width: 90%;
            position: relative;
            z-index: 10;
            animation: pulse 2s ease-in-out infinite alternate;
        }

        .progress-wrapper {
            margin: 30px 0;
        }

        .progress-bar {
            width: 100%;
            height: 30px;
            background: rgba(0, 0, 0, 0.8);
            border: 2px solid #00ff00;
            border-radius: 15px;
            position: relative;
            overflow: hidden;
        }

        .progress-fill {
            height: 100%;
            width: 0%;
            background: linear-gradient(90deg, #00ff00, #00ff41, #00ff00);
            border-radius: 13px;
            box-shadow: 0 0 20px rgba(0, 255, 0, 0.8);
            transition: width 0.3s ease;
        }

        .progress-text {
            color: #00ff00;
            font-size: 1.2rem;
            margin-top: 10px;
            text-shadow: 0 0 10px #00ff00;
            font-weight: bold;
        }

        .progress-scanner {
            position: absolute;
            top: 0;
            left: 0;
            height: 100%;
            width: 50px;
            background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.6), transparent);
            animation: scan 2s linear infinite;
        }

        @keyframes scan {
            0% { left: -50px; }
            100% { left: 100%; }
        }

        .loading-status {
            margin: 30px 0;
        }

        .status-line {
            color: #00ff00;
            font-size: 1rem;
            margin: 10px 0;
            text-shadow: 0 0 10px #00ff00;
        }

        /* Completion Container */
        .completion-container {
            text-align: center;
        }

        .md5-box {
            background: rgba(0, 0, 0, 0.8);
            border: 2px solid #00ff00;
            border-radius: 10px;
            padding: 20px;
            margin: 30px 0;
        }

        .md5-label {
            color: #00ff00;
            font-size: 0.9rem;
            margin-bottom: 10px;
            text-shadow: 0 0 10px #00ff00;
        }

        .md5-value {
            color: #00ff41;
            font-family: 'Courier New', monospace;
            font-size: 0.85rem;
            word-break: break-all;
            display: block;
            text-shadow: 0 0 5px #00ff41;
        }

        .anotha-button {
            background: linear-gradient(45deg, #ff0080, #ff41a0, #ff0080);
            border: none;
            padding: 20px 40px;
            font-size: 1rem;
            font-weight: 700;
            color: #000;
            border-radius: 15px;
            cursor: pointer;
            font-family: 'Orbitron', monospace;
            text-transform: uppercase;
            letter-spacing: 2px;
            position: relative;
            overflow: hidden;
            transition: all 0.3s ease;
            margin-top: 20px;
        }

        .anotha-button:hover {
            transform: scale(1.1);
            box-shadow: 0 0 30px rgba(255, 0, 128, 0.8);
        }

        .anotha-button:active {
            transform: scale(0.95);
        }

        .anotha-button::before {
            content: '';
            position: absolute;
            top: 0;
            left: -100%;
            width: 100%;
            height: 100%;
            background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.4), transparent);
            transition: left 0.5s;
        }

        .anotha-button:hover::before {
            left: 100%;
        }
    </style>
</head>
<body>
    <div class="matrix"></div>

    <div class="container" id="main-container">
        <h1>ZIP IT JIT! &#x1F525;</h1>
        <div class="subtitle">* GIGA FILE ENCRYPTION MECHANISM *</div>

        <div class="features">
            <div class="feature-item">&#x1F510;&#x1F5FF; AES-256 DOUBLE ZIP AND ENCRYPTED BRUH &#x1F5FF;&#x1F510;</div>
            <div class="feature-item">&#x1F1F0;&#x1F1F5;&#x1F680; MILITARY GRADE ENCRYPTION &#x1F1FA;&#x1F1F8;&#x1F680;</div>
            <div class="feature-item">
                &#x26A1; QUANTUM ENTANGLED PASSWORD SPAWNING &#x26A1;
                <div class="password-subtext">(password is 'password')</div>
            </div>
            <div class="feature-item">&#x1F9E0; ZERO KNOWLEDGE REQUIRED &#x1F9E0;</div>
        </div>

        <form id="upload-form" onsubmit="submitForm(event)">
            <div class="form-group">
                <input type="url" id="url-input" name="url" placeholder=" PASTE YOUR TOTALLY LEGAL FILE URL" required>
            </div>
            <button type="submit" class="hack-button">
                ENCRYPT MY RESEARCH
            </button>
        </form>

        <div class="disclaimer">
             <div class="disclaimer-line">NO CAP THIS IS BUSSIN FR FR</div>
             <div class="disclaimer-line">2GB LIMIT | WE ARE NOT RESPONSIBLE FOR YOUR CHOICES</div>
             <div class="disclaimer-line">PASSWORD: "password" (WE TOLD YOU, IT'S VERY SECURE)</div>
        </div>
    </div>

    <div class="container loading-container" id="loading-container" style="display: none;">
        <h1>ENCRYPTION IN PROGRESS</h1>
        <div class="progress-wrapper">
            <div class="progress-bar">
                <div class="progress-fill" id="progress-fill"></div>
                <div class="progress-scanner"></div>
            </div>
            <div class="progress-text" id="progress-text">0%</div>
        </div>
        <div class="loading-status">
            <p class="status-line" id="status-text">Starting...</p>
        </div>
        <div class="warning" style="animation: none; opacity: 0.7;">
            Please wait, this may take a moment...
        </div>
    </div>

    <div class="container completion-container" id="completion-container" style="display: none;">
        <h1>DOWNLOAD COMPLETE</h1>
        <div class="md5-box">
            <p class="md5-label">ORIGINAL FILE MD5:</p>
            <code id="md5-hash" class="md5-value">-</code>
        </div>
        <button class="anotha-button" onclick="reset()">
            &#x1F525; ANOTHA ONE &#x1F525;
        </button>
    </div>

    <script>
        let currentJobID = null;
        let pollInterval = null;

        async function submitForm(event) {
            event.preventDefault();

            const urlInput = document.getElementById('url-input');
            const url = urlInput.value.trim();

            if (!url) return;

            // Show loading
            document.getElementById('main-container').style.display = 'none';
            document.getElementById('loading-container').style.display = 'block';
            document.getElementById('completion-container').style.display = 'none';

            // Submit to server
            try {
                const formData = new FormData();
                formData.append('url', url);

                const response = await fetch('/ZIJ', {
                    method: 'POST',
                    body: formData
                });

                if (!response.ok) {
                    throw new Error('Failed to create job');
                }

                const data = await response.json();
                currentJobID = data.job_id;

                // Start polling
                startPolling(currentJobID);
            } catch (error) {
                alert('Error: ' + error.message);
                reset();
            }
        }

        function startPolling(jobID) {
            if (pollInterval) {
                clearInterval(pollInterval);
            }

            pollInterval = setInterval(async () => {
                try {
                    const response = await fetch('/ZIJ/progress/' + jobID);
                    if (!response.ok) {
                        throw new Error('Failed to get progress');
                    }

                    const job = await response.json();
                    updateProgress(job);

                    if (job.progress === 100 && job.status_text === 'Complete') {
                        clearInterval(pollInterval);
                        await downloadFile(jobID);
                        showCompletion(job);
                    } else if (job.error) {
                        clearInterval(pollInterval);
                        alert('Error: ' + job.error);
                        reset();
                    }
                } catch (error) {
                    console.error('Polling error:', error);
                }
            }, 100); // Poll every 100ms
        }

        function updateProgress(job) {
            const progressFill = document.getElementById('progress-fill');
            const progressText = document.getElementById('progress-text');
            const statusText = document.getElementById('status-text');

            progressFill.style.width = job.progress + '%';
            progressText.textContent = job.progress + '%';
            statusText.textContent = job.status_text || 'Processing...';
        }

        async function downloadFile(jobID) {
            // Auto-download via window.location
            window.location.href = '/ZIJ/download/' + jobID;
        }

        function showCompletion(job) {
            document.getElementById('loading-container').style.display = 'none';
            document.getElementById('completion-container').style.display = 'block';

            const md5Element = document.getElementById('md5-hash');
            md5Element.textContent = job.original_md5 || 'N/A';
        }

        function reset() {
            document.getElementById('main-container').style.display = 'block';
            document.getElementById('loading-container').style.display = 'none';
            document.getElementById('completion-container').style.display = 'none';

            // Clear form
            document.getElementById('url-input').value = '';

            // Reset progress
            document.getElementById('progress-fill').style.width = '0%';
            document.getElementById('progress-text').textContent = '0%';
            document.getElementById('status-text').textContent = 'Starting...';

            // Clear job ID
            currentJobID = null;

            if (pollInterval) {
                clearInterval(pollInterval);
                pollInterval = null;
            }
        }

        document.addEventListener('DOMContentLoaded', function() {
            // Particle effects
            for(let i = 0; i < 20; i++) {
                createParticle();
            }

            // Button hover effect
            const button = document.querySelector('.hack-button');
            if (button) {
                button.addEventListener('mouseenter', function() {
                    this.style.filter = 'hue-rotate(90deg)';
                    setTimeout(function() {
                        button.style.filter = 'hue-rotate(0deg)';
                    }, 200);
                });
            }
        });

        function createParticle() {
            const particle = document.createElement('div');
            particle.style.cssText = 'position: fixed; width: 4px; height: 4px; background: #00ff00; border-radius: 50%; pointer-events: none; z-index: 5; animation: float ' + (Math.random() * 3 + 2) + 's linear infinite; left: ' + (Math.random() * 100) + '%; top: ' + (Math.random() * 100) + '%;';
            document.body.appendChild(particle);

            setTimeout(function() {
                particle.remove();
                createParticle();
            }, (Math.random() * 3 + 2) * 1000);
        }

        const style = document.createElement('style');
        style.textContent = '@keyframes float { 0% { transform: translateY(0px) rotate(0deg); opacity: 1; } 100% { transform: translateY(-100vh) rotate(360deg); opacity: 0; } }';
        document.head.appendChild(style);
    </script>
</body>
</html>`)
}

func createJob(w http.ResponseWriter, r *http.Request) {
	// Rate limiting
	if !limiter.Allow() {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	downloadURL := strings.TrimSpace(r.FormValue("url"))
	if downloadURL == "" {
		http.Error(w, "URL required", http.StatusBadRequest)
		return
	}

	// Validate URL with DNS resolution
	if _, err := validateAndResolveURL(downloadURL); err != nil {
		http.Error(w, "Invalid URL: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Extract and validate filename
	originalFilename, err := extractAndValidateFilename(downloadURL)
	if err != nil {
		http.Error(w, "Invalid filename: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create job
	jobID := uuid.New().String()
	job := &Job{
		ID:         jobID,
		Progress:   0,
		StatusText: "Starting...",
		CreatedAt:  time.Now(),
	}

	jobsMutex.Lock()
	jobs[jobID] = job
	jobsMutex.Unlock()

	// Start background download
	go processDownload(jobID, downloadURL, originalFilename)

	// Return job ID as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
}

func serveProgress(w http.ResponseWriter, jobID string) {
	jobsMutex.RLock()
	job, exists := jobs[jobID]
	jobsMutex.RUnlock()

	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func serveDownload(w http.ResponseWriter, jobID string) {
	jobsMutex.RLock()
	job, exists := jobs[jobID]
	if !exists {
		jobsMutex.RUnlock()
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	zipPath := job.ZipPath
	jobsMutex.RUnlock()

	if zipPath == "" {
		http.Error(w, "Download not ready", http.StatusNotFound)
		return
	}

	// Serve file
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.zip", jobID))
	w.Header().Set("Content-Type", "application/zip")

	file, err := os.Open(zipPath)
	if err != nil {
		http.Error(w, "File serve failed", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	io.Copy(w, file)
	log.Printf("File served: %s", jobID)
}

func processDownload(jobID, downloadURL, originalFilename string) {
	defer func() {
		if r := recover(); r != nil {
			jobsMutex.Lock()
			if job, exists := jobs[jobID]; exists {
				job.Error = fmt.Sprintf("Panic: %v", r)
				job.StatusText = "Failed"
			}
			jobsMutex.Unlock()
			log.Printf("Job %s panicked: %v", jobID, r)
		}
	}()

	// Create secure temp file for download
	tempFile, err := os.CreateTemp("./temp", "download_*")
	if err != nil {
		jobsMutex.Lock()
		if job, exists := jobs[jobID]; exists {
			job.Error = "Failed to create temp file"
			job.StatusText = "Failed"
		}
		jobsMutex.Unlock()
		return
	}
	tempFileName := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempFileName)

	// Download with progress tracking and MD5
	md5Hash, err := secureDownloadFileWithProgress(downloadURL, tempFileName, jobID)
	if err != nil {
		jobsMutex.Lock()
		if job, exists := jobs[jobID]; exists {
			job.Error = err.Error()
			job.StatusText = "Download failed"
		}
		jobsMutex.Unlock()
		log.Printf("Job %s download failed: %v", jobID, err)
		return
	}

	// Update job with MD5
	jobsMutex.Lock()
	if job, exists := jobs[jobID]; exists {
		job.OriginalMD5 = md5Hash
		job.StatusText = "Zipping..."
		job.Progress = 90
	}
	jobsMutex.Unlock()

	// Create double zip
	zipFile := tempFileName + ".zip"
	guid, err := createDoubleZip(tempFileName, zipFile, originalFilename)
	if err != nil {
		jobsMutex.Lock()
		if job, exists := jobs[jobID]; exists {
			job.Error = "Zip creation failed"
			job.StatusText = "Failed"
		}
		jobsMutex.Unlock()
		log.Printf("Job %s zip failed: %v", jobID, err)
		return
	}

	// Update job as complete
	jobsMutex.Lock()
	if job, exists := jobs[jobID]; exists {
		job.Progress = 100
		job.StatusText = "Complete"
		job.ZipPath = zipFile
	}
	jobsMutex.Unlock()

	log.Printf("Job %s completed: %s (MD5: %s)", jobID, guid, md5Hash)
}

func processURL(w http.ResponseWriter, r *http.Request) {
	// Rate limiting
	if !limiter.Allow() {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	downloadURL := strings.TrimSpace(r.FormValue("url"))
	if downloadURL == "" {
		http.Error(w, "URL required", http.StatusBadRequest)
		return
	}

	// Validate and sanitize URL with DNS resolution
	if _, err := validateAndResolveURL(downloadURL); err != nil {
		http.Error(w, "Invalid URL: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Extract and validate filename
	originalFilename, err := extractAndValidateFilename(downloadURL)
	if err != nil {
		http.Error(w, "Invalid filename: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create secure temp file
	tempFile, err := os.CreateTemp("./temp", "download_*")
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	tempFile.Close()
	defer os.Remove(tempFile.Name())

	log.Printf("Received proper URL: %s", downloadURL)
	// Download with restrictions
	if err := secureDownloadFile(downloadURL, tempFile.Name()); err != nil {
		http.Error(w, "Download failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Rest of your zip logic...
	zipFile := tempFile.Name() + ".zip"
	guid, err := createDoubleZip(tempFile.Name(), zipFile, originalFilename)
	if err != nil {
		http.Error(w, "Zip creation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(zipFile)

	// Serve file
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.zip", guid))
	w.Header().Set("Content-Type", "application/zip")

	file, err := os.Open(zipFile)
	if err != nil {
		http.Error(w, "File serve failed", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	io.Copy(w, file)
	log.Printf("File served: %s", originalFilename)
}

func extractAndValidateFilename(downloadURL string) (string, error) {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return "", err
	}

	filename := filepath.Base(u.Path)

	// Fallback if no filename
	if filename == "" || filename == "." || filename == "/" {
		filename = "file"
	}

	// Length check
	if len(filename) > maxFilenameLen {
		filename = filename[:maxFilenameLen]
	}

	// Character validation
	if !filenameRegex.MatchString(filename) {
		return "", fmt.Errorf("filename contains invalid characters")
	}

	return filename, nil
}

func secureDownloadFileWithProgress(rawURL, filename, jobID string) (string, error) {
	// Validate URL and resolve IPs upfront
	_, err := validateAndResolveURL(rawURL)
	if err != nil {
		return "", err
	}

	// Create custom dialer that re-validates IPs at connection time
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}

	// Create HTTP client with custom transport
	client := &http.Client{
		Timeout: downloadTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Extract hostname from address
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}

				// Resolve and validate IP at connection time (prevents DNS rebinding)
				ips, err := net.LookupIP(host)
				if err != nil {
					return nil, err
				}

				// Check all resolved IPs
				for _, ip := range ips {
					if isBlockedIP(ip) {
						return nil, fmt.Errorf("connection blocked: resolves to blocked IP")
					}
				}

				// Proceed with connection
				return dialer.DialContext(ctx, network, addr)
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Limit redirects (reduced from 5 to 3)
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			// Re-validate each redirect with DNS resolution
			_, err := validateAndResolveURL(req.URL.String())
			return err
		},
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	// Get content length for progress tracking
	contentLength := resp.ContentLength
	if contentLength > maxFileSize {
		return "", fmt.Errorf("file too large")
	}

	// Create file
	out, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// Create MD5 hasher
	hasher := md5.New()

	// Create progress writer
	var writer io.Writer = out
	if contentLength > 0 {
		pw := &progressWriter{
			writer:       out,
			total:        contentLength,
			jobID:        jobID,
			statusPrefix: "Downloading...",
		}
		writer = pw
	}

	// Use TeeReader to hash while writing with progress
	limitedReader := io.LimitReader(resp.Body, maxFileSize+1)
	teeReader := io.TeeReader(limitedReader, hasher)

	written, err := io.Copy(writer, teeReader)
	if err != nil {
		return "", err
	}

	if written > maxFileSize {
		return "", fmt.Errorf("file exceeds size limit")
	}

	// Get MD5 hash
	md5Hash := hex.EncodeToString(hasher.Sum(nil))

	return md5Hash, nil
}

func secureDownloadFile(rawURL, filename string) error {
	// Validate URL and resolve IPs upfront
	_, err := validateAndResolveURL(rawURL)
	if err != nil {
		return err
	}

	// Create custom dialer that re-validates IPs at connection time
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}

	// Create HTTP client with custom transport
	client := &http.Client{
		Timeout: downloadTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Extract hostname from address
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}

				// Resolve and validate IP at connection time (prevents DNS rebinding)
				ips, err := net.LookupIP(host)
				if err != nil {
					return nil, err
				}

				// Check all resolved IPs
				for _, ip := range ips {
					if isBlockedIP(ip) {
						return nil, fmt.Errorf("connection blocked: resolves to blocked IP")
					}
				}

				// Proceed with connection
				return dialer.DialContext(ctx, network, addr)
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Limit redirects (reduced from 5 to 3)
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			// Re-validate each redirect with DNS resolution
			_, err := validateAndResolveURL(req.URL.String())
			return err
		},
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Create file
	out, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer out.Close()

	// Copy with size limit
	_, err = io.CopyN(out, resp.Body, maxFileSize)
	if err != nil && err != io.EOF {
		return err
	}

	return nil
}

func createDoubleZip(sourceFile, finalZipFile, originalFilename string) (string, error) {
	guid := uuid.New().String()

	// First zip: originalFilename -> guid.zip
	firstZip := sourceFile + "_1.zip"
	if err := createPasswordZip(sourceFile, firstZip, originalFilename, "password"); err != nil {
		return "", err
	}
	defer os.Remove(firstZip)

	// Second zip: guid.zip -> guid.zip (outer)
	if err := createPasswordZip(firstZip, finalZipFile, guid+".zip", "password"); err != nil {
		return "", err
	}

	return guid, nil
}

func createPasswordZip(sourceFile, zipFile, entryName, password string) error {
	out, err := os.Create(zipFile)
	if err != nil {
		return err
	}
	defer out.Close()

	archive := zip.NewWriter(out)
	defer archive.Close()

	file, err := os.Open(sourceFile)
	if err != nil {
		return err
	}
	defer file.Close()

	writer, err := archive.Encrypt(entryName, password)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	return err
}
