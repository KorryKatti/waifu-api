package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// # Probing
//
// One request per check, nothing more. A probe never walks an instance's
// catalogue, because the only question being asked is "will this work if I
// point a client at it", and a client asks exactly one question: can I get an
// image?

// Limits on every outbound request the prober makes.
//
// The response cap is the one that matters. A registered URL is untrusted
// input, and a handler that reads an unbounded body is a way to exhaust the
// process's memory from a URL someone else controls.
const (
	// ProbeTimeout bounds the whole probe: every request and every redirect.
	ProbeTimeout = 15 * time.Second

	// RequestTimeout bounds one request, so one slow endpoint cannot consume the
	// entire budget the others share.
	RequestTimeout = 8 * time.Second

	// MaxResponseBytes caps what is read. /version returns well under a
	// kilobyte; /images?pageSize=1 returns a list of one.
	MaxResponseBytes = 256 << 10

	// MaxRedirects stops a redirect chain being used to walk from a public host
	// to a blocked one. Each hop is re-checked, so this is a bound on effort
	// rather than the control itself.
	MaxRedirects = 3
)

// Prober fetches from an instance. The background job and the tests both use
// this interface, because the integration suite runs against a real database and
// must not make real HTTP calls to prove a handler validates its input.
type Prober interface {
	Probe(ctx context.Context, baseURL string) Probe
}

// HTTPProber is the real one: an outbound HTTP client with every guard on.
type HTTPProber struct {
	client *http.Client

	// allowBlocked lifts the address block list.
	//
	// It is unexported and exists for one reason: httptest serves on loopback,
	// which is exactly the range the prober refuses, so without a way to reach
	// it there is no way to test any of this. Nothing outside this package can
	// set it, so a deployment cannot turn the guard off by configuration or by
	// accident.
	allowBlocked bool
}

// NewHTTPProber builds a prober whose transport refuses to dial a blocked
// address.
//
// The check is in DialContext rather than in a handler-level validation step,
// because a hostname can resolve differently between validation and use, and
// only the address actually dialled is the one that matters.
func NewHTTPProber() *HTTPProber {
	p := &HTTPProber{}

	p.client = &http.Client{
		Transport: &http.Transport{DialContext: p.dialContext},
		Timeout:   ProbeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", MaxRedirects)
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("refusing to follow a redirect to %q", req.URL.Scheme)
			}
			return nil
		},
	}
	return p
}

// dialContext is the only place an address is turned into a connection, so it is
// the only place the block list has to be enforced.
func (p *HTTPProber) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: RequestTimeout, KeepAlive: 30 * time.Second}

	if p.allowBlocked {
		return dialer.DialContext(ctx, network, address)
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	// A literal address needs no resolution, and checking it here is the only
	// place a direct-to-IP registration is caught.
	if net.ParseIP(host) != nil {
		if ResolveBlockedIP(net.ParseIP(host)) {
			return nil, BlockedHostError{Host: host, Reason: "address is private, loopback or link-local"}
		}
		return dialer.DialContext(ctx, network, address)
	}

	// Resolve first, then dial the address ourselves. Letting the dialer resolve
	// would mean the decision is made after the connection is already open.
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}

	var lastErr error
	for _, candidate := range addresses {
		if ResolveBlockedIP(candidate.IP) {
			lastErr = BlockedHostError{Host: host, Reason: "host resolves to a private, loopback or link-local address"}
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("host %s resolved to no addresses", host)
	}
	return nil, lastErr
}

// Probe runs the three behavioural checks against one instance.
//
// The returned Probe is always usable: an unreachable instance produces one with
// Reachable false and an Error, never an error return, because "we could not
// reach it" is the finding rather than a failure to find anything.
func (p *HTTPProber) Probe(ctx context.Context, baseURL string) Probe {
	result := Probe{CheckedAt: time.Now().UTC()}

	target, err := Normalise(baseURL)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	handshake, err := p.version(ctx, target)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Reachable = true
	result.Software = handshake.Software
	result.Version = handshake.Version
	result.Commit = handshake.Commit
	result.BuiltAt = handshake.BuiltAt
	result.BuiltAgeDays = builtAgeDays(handshake.BuiltAt, result.CheckedAt)

	imageURL, shapeErr := p.image(ctx, target)

	// An instance with an empty catalogue answered correctly. The shape was
	// readable, so schema is true; there is simply no image to check, which
	// leaves the image signal unsatisfied rather than the response malformed.
	if errors.Is(shapeErr, errEmptyCatalogue) {
		result.Schema = true
		result.Error = shapeErr.Error()
		return result
	}
	if shapeErr != nil {
		result.Error = shapeErr.Error()
		return result
	}
	result.Schema = true

	if err := p.serveable(ctx, imageURL); err != nil {
		result.Error = err.Error()
		return result
	}

	result.Images = true
	return result
}

// ResolveUpstream fills in the genealogical fields from the bundled history.
//
// It is a separate step from Probe because it is the only part that needs this
// repository rather than the instance, and because it is the only place a commit
// is resolved — so it is the only place that can return "not upstream" and leave
// the derived fields absent.
//
// It is a method rather than a free function so the prober stays substitutable:
// a caller that probes with a fake gets no genealogy, which is correct, because
// a fake is not making a request to a real instance that reported a real commit.
func (p *HTTPProber) ResolveUpstream(probe *Probe, now time.Time) {
	if probe == nil || probe.Commit == "" {
		return
	}

	resolved, age := ResolveCommit(probe.Commit, now)
	if !resolved {
		return
	}

	upstream := true
	probe.Upstream = &upstream
	probe.CommitAgeDays = &age
}

// handshake is the parsed body of GET /version.
type handshake struct {
	Software string `json:"software"`
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	BuiltAt  string `json:"builtAt"`
}

func (p *HTTPProber) version(ctx context.Context, base string) (handshake, error) {
	body, err := p.get(ctx, base+"/version")
	if err != nil {
		return handshake{}, fmt.Errorf("GET /version: %w", err)
	}

	var parsed handshake
	if err := json.Unmarshal(body, &parsed); err != nil {
		return handshake{}, fmt.Errorf("GET /version: response is not the expected shape: %w", err)
	}
	if parsed.Software == "" {
		return handshake{}, fmt.Errorf("GET /version: response does not name the software")
	}
	return parsed, nil
}

// image fetches one image listing and returns the URL of its first entry.
//
// The shape check is structural rather than a version comparison: the point is
// whether a client can read the response, not whether the instance runs the same
// release as this one.
func (p *HTTPProber) image(ctx context.Context, base string) (string, error) {
	body, err := p.get(ctx, base+"/images?pageSize=1")
	if err != nil {
		return "", fmt.Errorf("GET /images: %w", err)
	}

	var listing struct {
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return "", fmt.Errorf("GET /images: response is not the expected shape: %w", err)
	}
	if len(listing.Images) == 0 {
		// An instance with no images is a working instance. It is not evidence
		// that the catalogue is readable, so the check is inconclusive rather
		// than failed, and the image signal stays false without claiming a
		// fault.
		return "", errEmptyCatalogue
	}
	if listing.Images[0].URL == "" {
		return "", fmt.Errorf("GET /images: the first entry carries no url")
	}
	return listing.Images[0].URL, nil
}

// errEmptyCatalogue means the instance served a well-formed empty listing.
var errEmptyCatalogue = errors.New("the instance returned no images to check")

// serveable checks that the image URL actually resolves.
//
// This is the check that earns the word "trust". It exercises the
// Image.Extension to {id}{ext} key invariant end to end: an instance whose rows
// name a format the bucket does not hold returns a URL for an object that is
// gone, which is a 404 handed to a client rather than to the bucket. Nothing
// else in this probe would notice.
func (p *HTTPProber) serveable(ctx context.Context, imageURL string) error {
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return fmt.Errorf("image url is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("image url has unsupported scheme %q", parsed.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, imageURL, nil)
	if err != nil {
		return fmt.Errorf("HEAD %s: %w", imageURL, err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("HEAD %s: %w", imageURL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HEAD %s: %s", imageURL, resp.Status)
	}
	return nil
}

// get performs one bounded GET and returns the capped body.
func (p *HTTPProber) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}

	// One byte over the cap is enough to tell truncation from a complete read,
	// so the limit is not quietly producing valid-looking JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", MaxResponseBytes)
	}
	return body, nil
}

// builtAgeDays is how long ago the deployment was built, or nil when the
// instance reported nothing parseable.
//
// Nil rather than 0: an instance whose build timestamp cannot be read has not
// been built "now", and reporting 0 days would make an unknown look like the
// freshest entry in the list.
func builtAgeDays(builtAt string, now time.Time) *int {
	if builtAt == "" {
		return nil
	}

	parsed, err := time.Parse(time.RFC3339, builtAt)
	if err != nil {
		return nil
	}

	days := int(now.Sub(parsed).Hours() / 24)
	if days < 0 {
		// A build timestamp in the future is a clock problem, not an instance
		// that has been running for negative time.
		days = 0
	}
	return &days
}
