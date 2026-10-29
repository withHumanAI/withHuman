package webhookdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/withHumanAI/withHuman/telemetry"
)

const (
	UserAgent            = "withHuman-webhooks/1"
	MaximumResponseBytes = 64 * 1024
	DefaultTimeout       = 5 * time.Second
	dialTimeout          = 10 * time.Second
)

// ErrResponseTooLarge means the endpoint answered with more than the
// response limit; the answer is discarded unread.
var ErrResponseTooLarge = errors.New("response exceeds the size limit")

// Delivery is one signed POST. Timeout bounds the attempt from connect to
// the full response. Secret signs with withHuman's own scheme; a caller
// that signs another way (AAP's Standard Webhooks) leaves it empty and
// supplies its headers in Headers.
type Delivery struct {
	// Purpose says why the webhook is sent, one of the telemetry
	// package's webhook purposes, for the delivery metric.
	Purpose        string
	URL            string
	Secret         string
	Headers        http.Header
	Body           []byte
	Timeout        time.Duration
	DeliveryID     string
	IdempotencyKey string
}

// Response is what came back. A transport failure returns an error and a
// Response carrying only the duration.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Duration   time.Duration
}

type resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Client delivers signed payloads under the egress policy. Destinations
// are resolved before dialing, every resolved address must pass the
// policy, and the validated address is dialed directly so a DNS answer
// cannot change between check and connect. When the environment names a
// proxy for the destination, the proxy makes the egress and pinning does
// not apply; the static URL policy still does.
type Client struct {
	policy   Policy
	pinned   *http.Transport
	proxied  *http.Transport
	resolver resolver
	now      func() time.Time
}

func NewClient(policy Policy) *Client {
	client := &Client{policy: policy, resolver: net.DefaultResolver, now: time.Now}
	dialer := &net.Dialer{Timeout: dialTimeout}
	client.pinned = &http.Transport{
		Proxy: nil, DialContext: client.dialPinned(dialer),
		TLSHandshakeTimeout: dialTimeout, ForceAttemptHTTP2: true,
		MaxIdleConns: 16, IdleConnTimeout: time.Minute,
	}
	client.proxied = &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: dialer.DialContext,
		TLSHandshakeTimeout: dialTimeout, ForceAttemptHTTP2: true,
		MaxIdleConns: 16, IdleConnTimeout: time.Minute,
	}
	return client
}

func (c *Client) dialPinned(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var candidates []net.IP
		if ip := net.ParseIP(host); ip != nil {
			candidates = []net.IP{ip}
		} else {
			resolved, err := c.resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, entry := range resolved {
				candidates = append(candidates, entry.IP)
			}
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("%s resolved to no addresses", host)
		}
		for _, ip := range candidates {
			if !c.policy.AllowIP(ip) {
				return nil, fmt.Errorf("%s resolves to a blocked address: %w", host, ErrDestinationBlocked)
			}
		}
		var lastErr error
		for _, ip := range candidates {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// Deliver posts the body once. Redirects are not followed: a 3xx comes
// back as the response. The body is read through the response limit.
func (c *Client) Deliver(ctx context.Context, delivery Delivery) (Response, error) {
	target, err := c.policy.ValidateURL(delivery.URL)
	if err != nil {
		return Response{}, err
	}
	timeout := delivery.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	attemptContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptContext, http.MethodPost, target.String(), bytes.NewReader(delivery.Body))
	if err != nil {
		return Response{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", UserAgent)
	if delivery.Secret != "" {
		request.Header.Set(SignatureHeader, Sign(delivery.Secret, c.now().UTC(), delivery.Body))
	}
	for name, values := range delivery.Headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if delivery.DeliveryID != "" {
		request.Header.Set(DeliveryIDHeader, delivery.DeliveryID)
	}
	if delivery.IdempotencyKey != "" {
		request.Header.Set(IdempotencyKeyHeader, delivery.IdempotencyKey)
	}
	started := time.Now()
	response, err := c.send(request, MaximumResponseBytes)
	response.Duration = time.Since(started)
	telemetry.WebhookDelivered(ctx, delivery.Purpose, response.StatusCode, err != nil, IsTimeout(err), response.Duration)
	return response, err
}

// Fetch is one GET of a public document, such as an OAuth client's
// metadata document, under the same egress policy as a delivery.
// MaximumBytes bounds the body; zero means the delivery limit.
type Fetch struct {
	URL          string
	Accept       string
	Timeout      time.Duration
	MaximumBytes int64
}

// Fetch gets the document once. Like Deliver it follows no redirects and
// reads the body through the limit. It is not a webhook, so it records
// no delivery metric.
func (c *Client) Fetch(ctx context.Context, fetch Fetch) (Response, error) {
	target, err := c.policy.ValidateURL(fetch.URL)
	if err != nil {
		return Response{}, err
	}
	timeout := fetch.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	limit := fetch.MaximumBytes
	if limit <= 0 {
		limit = MaximumResponseBytes
	}
	attemptContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptContext, http.MethodGet, target.String(), nil)
	if err != nil {
		return Response{}, err
	}
	request.Header.Set("User-Agent", UserAgent)
	if fetch.Accept != "" {
		request.Header.Set("Accept", fetch.Accept)
	}
	started := time.Now()
	response, err := c.send(request, limit)
	response.Duration = time.Since(started)
	return response, err
}

// send makes the request through the pinned transport (or the proxy the
// environment names) without following redirects, and reads at most
// limit bytes of the body. A transport failure answers an empty Response;
// an oversized body answers the status and headers without it.
func (c *Client) send(request *http.Request, limit int64) (Response, error) {
	transport := c.pinned
	if proxyURL, proxyErr := http.ProxyFromEnvironment(request); proxyErr == nil && proxyURL != nil {
		transport = c.proxied
	}
	httpClient := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = ErrResponseTooLarge
	}
	if err != nil {
		return Response{StatusCode: response.StatusCode, Header: response.Header}, err
	}
	return Response{StatusCode: response.StatusCode, Header: response.Header, Body: body}, nil
}

// IsTimeout reports whether a delivery error was the attempt deadline.
func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
