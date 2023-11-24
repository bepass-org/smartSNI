package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/miekg/dns"
	"github.com/valyala/fasthttp"
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

var (
	// BufferPool for reuse of byte slices
	BufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 4096) // Adjust the size according to your needs
		},
	}
	config  *Config
	limiter *rate.Limiter
)

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
		if strings.Contains(strings.ToLower(substr), strings.ToLower(key)) {
			return value, true
		}
	}
	return "", false // Return empty string and false if no key contains the substring
}

// processDNSQuery processes the DNS query and returns a response.
func processDNSQuery(query []byte) ([]byte, error) {
	var msg dns.Msg
	err := msg.Unpack(query)
	if err != nil {
		return nil, err
	}

	if len(msg.Question) == 0 {
		return nil, fmt.Errorf("no DNS question found in the request")
	}

	domain := msg.Question[0].Name
	if ip, ok := findValueByKeyContains(config.Domains, domain); ok {
		hdr := dns.RR_Header{
			Name:   domain,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    3600, // example TTL
		}
		rr := &dns.A{
			Hdr: hdr,
			A:   net.ParseIP(ip),
		}
		if rr.A == nil {
			return nil, fmt.Errorf("invalid IP address")
		}
		msg.Answer = append(msg.Answer, rr)
		msg.SetReply(&msg) // Set appropriate flags and sections
		return msg.Pack()
	}

	resp, err := http.Post("https://1.1.1.1/dns-query", "application/dns-message", bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Use a fixed-size buffer from the pool for the initial read
	buffer := BufferPool.Get().([]byte)
	defer BufferPool.Put(buffer)

	// Read the initial chunk of the response
	n, err := resp.Body.Read(buffer)
	if err != nil && err != io.EOF {
		return nil, err
	}

	// If the buffer was large enough to hold the entire response, return it
	if n < len(buffer) {
		return buffer[:n], nil
	}

	// If the response is larger than our buffer, we need to read the rest
	// and append to a dynamically-sized buffer
	var dynamicBuffer bytes.Buffer
	dynamicBuffer.Write(buffer[:n])
	_, err = dynamicBuffer.ReadFrom(resp.Body)
	if err != nil {
		return nil, err
	}

	return dynamicBuffer.Bytes(), nil
}

// handleDoTConnection handles a single DoT connection.
func handleDoTConnection(conn net.Conn) {
	defer conn.Close()

	if !limiter.Allow() {
		log.Println("limit exceeded")
		return
	}

	// Use a fixed-size buffer from the pool for the initial read
	poolBuffer := BufferPool.Get().([]byte)
	defer BufferPool.Put(poolBuffer)

	// Read the first two bytes to determine the length of the DNS message
	_, err := io.ReadFull(conn, poolBuffer[:2])
	if err != nil {
		log.Println(err)
		return
	}

	// Parse the length of the DNS message
	dnsMessageLength := binary.BigEndian.Uint16(poolBuffer[:2])

	// Prepare a buffer to read the full DNS message
	var buffer []byte
	if int(dnsMessageLength) > len(poolBuffer) {
		// If pool buffer is too small, allocate a new buffer
		buffer = make([]byte, dnsMessageLength)
	} else {
		// Use the pool buffer directly
		buffer = poolBuffer[:dnsMessageLength]
	}

	// Read the DNS message
	_, err = io.ReadFull(conn, buffer)
	if err != nil {
		log.Println(err)
		return
	}

	// Process the DNS query and generate a response
	response, err := processDNSQuery(buffer)
	if err != nil {
		log.Println(err)
		return
	}

	// Prepare the response with the length header
	responseLength := make([]byte, 2)
	binary.BigEndian.PutUint16(responseLength, uint16(len(response)))

	// Write the length of the response followed by the response itself
	_, err = conn.Write(responseLength)
	if err != nil {
		log.Println(err)
		return
	}

	_, err = conn.Write(response)
	if err != nil {
		log.Println(err)
		return
	}
}

// startDoTServer starts the DNS-over-TLS server.
func startDoTServer() {
	// Load TLS credentials
	certPrefix := "/etc/letsencrypt/live/" + config.Host + "/"
	cer, err := tls.LoadX509KeyPair(certPrefix+"/fullchain.pem", certPrefix+"privkey.pem")
	if err != nil {
		log.Fatal(err)
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cer}}

	listener, err := tls.Listen("tcp", ":853", tlsConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go handleDoTConnection(conn)
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
			log.Println(err)
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
	return hello, peekedBytes, nil
}

type readOnlyConn struct {
	reader io.Reader
}

func (conn readOnlyConn) Read(p []byte) (int, error)         { return conn.reader.Read(p) }
func (conn readOnlyConn) Write(_ []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (conn readOnlyConn) Close() error                       { return conn.Close() }
func (conn readOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn readOnlyConn) SetDeadline(t time.Time) error      { return conn.SetDeadline(t) }
func (conn readOnlyConn) SetReadDeadline(t time.Time) error  { return conn.SetReadDeadline(t) }
func (conn readOnlyConn) SetWriteDeadline(t time.Time) error { return conn.SetWriteDeadline(t) }

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo
	var wg sync.WaitGroup

	// Set the wait group for one operation (Handshake)
	wg.Add(1)

	config := &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = argHello // Capture the ClientHelloInfo
			wg.Done()        // Indicate that the handshake is complete
			return nil, nil
		},
	}

	tlsConn := tls.Server(readOnlyConn{reader: reader}, config)
	err := tlsConn.Handshake()

	// Wait for the handshake to be captured
	wg.Wait()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Println(err)
		return
	}

	clientHello, clientHelloBytes, err := peekClientHello(clientConn)
	if err != nil {
		log.Println(err)
		return
	}

	if strings.TrimSpace(clientHello.ServerName) == "" {
		log.Println("empty sni not allowed here")
		// HTTP response headers and body
		response := "HTTP/1.1 502 OK\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"Content-Length: 21\r\n" +
			"\r\n" +
			"nginx, malformed data"

		// Write the response to the connection
		_, err := clientConn.Write([]byte(response))
		if err != nil {
			log.Println("Error writing response:", err)
		}
		return
	}

	targetHost := strings.ToLower(clientHello.ServerName)

	if targetHost == config.Host {
		targetHost = "127.0.0.1:8443"
	} else {
		targetHost = net.JoinHostPort(targetHost, "443")
	}

	backendConn, err := net.DialTimeout("tcp", targetHost, 5*time.Second)
	if err != nil {
		log.Println(err)
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
		io.Copy(backendConn, clientHelloBytes)
		io.Copy(backendConn, clientConn)
		backendConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()

	wg.Wait()
}

// handleDoHRequest processes the DoH request with rate limiting using fasthttp.
func handleDoHRequest(ctx *fasthttp.RequestCtx) {
	if !limiter.Allow() {
		ctx.Error("Rate limit exceeded", fasthttp.StatusTooManyRequests)
		return
	}

	var body []byte
	var err error

	switch string(ctx.Method()) {
	case "GET":
		dnsQueryParam := ctx.QueryArgs().Peek("dns")
		if dnsQueryParam == nil {
			ctx.Error("Missing 'dns' query parameter", fasthttp.StatusBadRequest)
			return
		}
		body, err = base64.RawURLEncoding.DecodeString(string(dnsQueryParam))
		if err != nil {
			ctx.Error("Invalid 'dns' query parameter", fasthttp.StatusBadRequest)
			return
		}
	case "POST":
		body = ctx.PostBody()
		if len(body) == 0 {
			ctx.Error("Empty request body", fasthttp.StatusBadRequest)
			return
		}
	default:
		ctx.Error("Only GET and POST methods are allowed", fasthttp.StatusMethodNotAllowed)
		return
	}

	dnsResponse, err := processDNSQuery(body)
	if err != nil {
		ctx.Error("Failed to process DNS query", fasthttp.StatusInternalServerError)
		return
	}

	ctx.SetContentType("application/dns-message")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Write(dnsResponse)
}

// runDOHServer starts the DNS-over-HTTPS server using fasthttp.
func runDOHServer() {
	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			switch string(ctx.Path()) {
			case "/dns-query":
				handleDoHRequest(ctx)
			default:
				ctx.Error("Unsupported path", fasthttp.StatusNotFound)
			}
		},
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if err := server.ListenAndServe("127.0.0.1:8080"); err != nil {
		log.Fatalf("Error in DoH Server: %s", err)
	}
}

func main() {
	err := os.Setenv("GOGC", "50")
	if err != nil {
		log.Fatal(err)
	} // Set GOGC to 50 to make GC more aggressive

	cfg, err := LoadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	config = cfg

	log.Println("Starting SSNI proxy server on :443, :853...")

	var wg sync.WaitGroup
	wg.Add(3)

	limiter = rate.NewLimiter(10, 50) // 1 request per second with a burst size of 5

	go func() {
		runDOHServer()
		wg.Done()
	}()
	go func() {
		startDoTServer()
		wg.Done()
	}()
	go func() {
		serveSniProxy()
		wg.Done()
	}()

	wg.Wait()
}
