package main

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	listenAddr        = "127.0.0.1:19877"
	upstreamURL       = "http://127.0.0.1:19876"
	authRealm         = `Basic realm="daemon-gui", charset="UTF-8"`
	expectedUsername  = "almanac"
	passwordHash      = "$2a$12$kt6QuR/UN3n2iMihBlxik.nCTAYyQXpJdObmYiAP318zkFTgC3Ywu"
	maxFailures       = 5
	failureWindow     = 60 * time.Second
	blockDuration     = 5 * time.Minute
	securityHeaderXFO = "DENY"
)

type failureState struct {
	attempts     []time.Time
	blockedUntil time.Time
}

type rateLimiter struct {
	mu     sync.Mutex
	states map[string]*failureState
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{states: make(map[string]*failureState)}
}

func (r *rateLimiter) isBlocked(ip string, now time.Time) (bool, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := r.states[ip]
	if state == nil {
		return false, time.Time{}
	}

	if !state.blockedUntil.IsZero() && now.Before(state.blockedUntil) {
		return true, state.blockedUntil
	}

	if !state.blockedUntil.IsZero() && !now.Before(state.blockedUntil) && len(state.attempts) == 0 {
		delete(r.states, ip)
		return false, time.Time{}
	}

	return false, time.Time{}
}

func (r *rateLimiter) recordFailure(ip string, now time.Time) (blocked bool, until time.Time, attempts int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := r.states[ip]
	if state == nil {
		state = &failureState{}
		r.states[ip] = state
	}

	state.attempts = pruneAttempts(state.attempts, now)
	state.attempts = append(state.attempts, now)
	attempts = len(state.attempts)
	if attempts >= maxFailures {
		state.blockedUntil = now.Add(blockDuration)
		state.attempts = nil
		return true, state.blockedUntil, attempts
	}

	return false, time.Time{}, attempts
}

func (r *rateLimiter) reset(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, ip)
}

func pruneAttempts(attempts []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-failureWindow)
	kept := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	return kept
}

func main() {
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		log.Fatalf("parse upstream: %v", err)
	}

	limiter := newRateLimiter()
	proxy := newProxy(upstream)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		ip := clientIP(r)
		setSecurityHeaders(w)

		if blocked, until := limiter.isBlocked(ip, now); blocked {
			log.Printf("ts=%s ip=%s result=rate_limited method=%s path=%q blocked_until=%s", now.Format(time.RFC3339), ip, r.Method, r.URL.RequestURI(), until.Format(time.RFC3339))
			http.Error(w, "too many failed authentication attempts", http.StatusTooManyRequests)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok || username != expectedUsername || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
			w.Header().Set("WWW-Authenticate", authRealm)
			blocked, until, attempts := limiter.recordFailure(ip, now)
			if blocked {
				log.Printf("ts=%s ip=%s result=auth_fail_blocked method=%s path=%q attempts=%d blocked_until=%s", now.Format(time.RFC3339), ip, r.Method, r.URL.RequestURI(), attempts, until.Format(time.RFC3339))
				http.Error(w, "too many failed authentication attempts", http.StatusTooManyRequests)
				return
			}
			log.Printf("ts=%s ip=%s result=auth_fail method=%s path=%q attempts=%d", now.Format(time.RFC3339), ip, r.Method, r.URL.RequestURI(), attempts)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		limiter.reset(ip)
		log.Printf("ts=%s ip=%s result=auth_success method=%s path=%q", now.Format(time.RFC3339), ip, r.Method, r.URL.RequestURI())
		proxy.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("daemon GUI proxy listening on %s forwarding to %s", listenAddr, upstreamURL)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}

func newProxy(upstream *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Scheme = upstream.Scheme
		req.URL.Host = upstream.Host
		req.Host = upstream.Host
		req.URL.Path, req.URL.RawPath = rewritePath(req.URL.Path, req.URL.RawPath)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("ts=%s ip=%s result=proxy_error method=%s path=%q err=%q", time.Now().UTC().Format(time.RFC3339), clientIP(r), r.Method, r.URL.RequestURI(), err.Error())
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	return proxy
}

func rewritePath(path, rawPath string) (string, string) {
	newPath := stripDaemonPrefix(path)
	newRawPath := rawPath
	if rawPath != "" {
		newRawPath = stripDaemonPrefix(rawPath)
	}
	return newPath, newRawPath
}

func stripDaemonPrefix(path string) string {
	switch {
	case path == "/daemon":
		return "/"
	case strings.HasPrefix(path, "/daemon/"):
		return strings.TrimPrefix(path, "/daemon")
	default:
		if path == "" {
			return "/"
		}
		return path
	}
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", securityHeaderXFO)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
			return cfIP
		}
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				return strings.TrimSpace(parts[0])
			}
		}
	}

	return host
}
