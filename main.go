package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"github.com/miekg/dns"
	"golang.org/x/time/rate"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var config *Config

// Config represents the structure of the configuration file.
type Config struct {
	Host    string            `json:"host"`
	Domains map[string]string `json:"domains"`
}

// LoadConfig loads the configuration from a JSON file.
func LoadConfig(filename string) (*Config, error) {
	var config Config
	cfgBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(cfgBytes, &config)
	return &config, err
}

func findValueByKeyContains(m map[string]string, substr string) (string, bool) {
	for key, value := range m {
		if strings.Contains(strings.ToLower(key), strings.ToLower(substr)) {
			return value, true
		}
	}
	return "", false // Return empty string and false if no key contains the substring
}

// handleDoHRequest processes the DoH request with rate limiting.
func handleDoHRequest(limiter *rate.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}

		var msg dns.Msg
		err = msg.Unpack(body)
		if err != nil {
			http.Error(w, "Failed to unpack DNS message", http.StatusBadRequest)
			return
		}

		if len(msg.Question) == 0 {
			http.Error(w, "No DNS question found in the request", http.StatusBadRequest)
			return
		}

		domain := msg.Question[0].Name
		if ip, ok := findValueByKeyContains(config.Domains, domain); ok {
			rr, err := dns.NewRR(domain + " A " + ip)
			if err != nil {
				http.Error(w, "Failed to create DNS resource record", http.StatusInternalServerError)
				return
			}
			msg.Answer = append(msg.Answer, rr)
		} else {
			resp, err := http.Post("https://1.1.1.1/dns-query", "application/dns-message", bytes.NewReader(body))
			if err != nil {
				http.Error(w, "Failed to forward request", http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()
			forwardedBody, _ := io.ReadAll(resp.Body)
			w.Header().Set("Content-Type", "application/dns-message")
			w.Write(forwardedBody)
			return
		}

		dnsResponse, err := msg.Pack()
		if err != nil {
			http.Error(w, "Failed to pack DNS response", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(dnsResponse)
	}
}

func serveSniProxy() {
	l, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Print(err)
			continue
		}
		go handleConnection(conn)
	}
}

func peekClientHello(reader io.Reader) (*tls.ClientHelloInfo, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	hello, err := readClientHello(io.TeeReader(reader, peekedBytes))
	if err != nil {
		return nil, nil, err
	}
	return hello, io.MultiReader(peekedBytes, reader), nil
}

type readOnlyConn struct {
	reader io.Reader
}

func (conn readOnlyConn) Read(p []byte) (int, error)         { return conn.reader.Read(p) }
func (conn readOnlyConn) Write(p []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (conn readOnlyConn) Close() error                       { return nil }
func (conn readOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn readOnlyConn) SetDeadline(t time.Time) error      { return nil }
func (conn readOnlyConn) SetReadDeadline(t time.Time) error  { return nil }
func (conn readOnlyConn) SetWriteDeadline(t time.Time) error { return nil }

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo

	err := tls.Server(readOnlyConn{reader: reader}, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = new(tls.ClientHelloInfo)
			*hello = *argHello
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Print(err)
		return
	}

	clientHello, clientReader, err := peekClientHello(clientConn)
	if err != nil {
		log.Print(err)
		return
	}

	if err := clientConn.SetReadDeadline(time.Time{}); err != nil {
		log.Print(err)
		return
	}

	targetHost := strings.ToLower(clientHello.ServerName)

	if !strings.HasSuffix(targetHost, ".internal.example.com") {
		log.Print("Blocking connection to unauthorized backend")
		return
	}

	if targetHost == config.Host {
		targetHost = net.JoinHostPort(targetHost, "8443")
	} else {
		targetHost = net.JoinHostPort(targetHost, "443")
	}

	backendConn, err := net.DialTimeout("tcp", targetHost, 5*time.Second)
	if err != nil {
		log.Print(err)
		return
	}
	defer backendConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(clientConn, backendConn)
		clientConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()
	go func() {
		io.Copy(backendConn, clientReader)
		backendConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()

	wg.Wait()
}

func runDOHServer() {
	limiter := rate.NewLimiter(1, 5) // 1 request per second with a burst size of 5

	http.HandleFunc("/dns-query", handleDoHRequest(limiter))

	server := &http.Server{
		Addr:         "127.0.0.1:8080",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Println("Starting DoH proxy server on :8080...")
	log.Fatal(server.ListenAndServe())
}

func main() {
	cfg, err := LoadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	config = cfg

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		runDOHServer()
		wg.Done()
	}()
	go func() {
		serveSniProxy()
		wg.Done()
	}()

	wg.Wait()
}
