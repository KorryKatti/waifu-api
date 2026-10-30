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
		Transport: &http.Transport{
			DialContext: p.dialContext,

			// These are resource limits, and this process runs on a 512 MB free
			// tier. IdleConnTimeout matters most: without it a connection
			// outlives the probe that made it, so the pool accumulates across
			// days rather than shrinking between them. A handful of sockets, but
			// it is free to bound them.
			MaxIdleConns:          8,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   RequestTimeout,
			ExpectContinueTimeout: time.Second,
		},
		Timeout: ProbeTimeout,
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
		var status statusError
		if !errors.As(err, &status) {
			// Nothing answered at all: there is no second question to ask, and
			// a client pointed here is experiencing downtime.
			result.Error = err.Error()
			return result
		}

		// It answered with a status rather than failing to answer, so the
		// checks that do not depend on /version are still worth making.
		//
		// There are two kinds of status, and they mean different things. A
		// refusal — 401, 403, 429 — says nothing about the contract: the
		// instance declined to let this checker look, usually a firewall or bot
		// rule aimed at the prober's address, and a client elsewhere may be
		// served perfectly. Anything else is the contract mismatch this
		// endpoint was built to tolerate.
		result.Error = err.Error()
		if status.refused() {
			result.Refused = true
		} else {
			result.NoHandshake = true
		}
	} else {
		result.Reachable = true
		result.Software = handshake.Software
		result.Version = handshake.Version
		result.Commit = handshake.Commit
		result.BuiltAt = handshake.BuiltAt
		result.BuiltAgeDays = builtAgeDays(handshake.BuiltAt, result.CheckedAt)
	}

	imageURL, shapeErr := p.image(ctx, target)

	// An instance with an empty catalogue answered correctly. The shape was
	// readable, so schema is true; there is simply no image to check, which
	// leaves the image signal unsatisfied rather than the response malformed.
	if errors.Is(shapeErr, errEmptyCatalogue) {
		result.Schema = true
		result.NoImages = true
		result.Error = shapeErr.Error()
		return result
	}
	if shapeErr != nil {
		var status statusError
		if errors.As(shapeErr, &status) && status.refused() {
			// Refused rather than broken. The signal stays unobserved, and the
			// reason is carried so the index can say why instead of implying a
			// failure nobody measured.
			result.Refused = true
		}
		result.Error = shapeErr.Error()
		return result
	}
	result.Schema = true

	if err := p.serveable(ctx, imageURL); err != nil {
		var refused refusedError
		if errors.As(err, &refused) {
			result.Refused = true
		}
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
	return ParseImageListing(body)
}

// ParseImageListing reads a GET /images response and returns the URL of its
// first entry.
//
// It is exported so the shape can be checked against the response the images
// handler actually produces, rather than against a fixture somebody wrote from
// memory. An earlier version of this parsed `{"images": [...]}` while the
// endpoint answers `{"items": [...], "totalCount": ..., ...}`, and every
// registration was refused with a schema failure - the prober and its own test
// fixture shared the same wrong assumption, so nothing caught it. The round-trip
// test in internal/httpapi is what makes that class of mistake fail.
//
// An empty list is errEmptyCatalogue rather than a failure: the response was
// readable, the instance simply has nothing in it to serve.
func ParseImageListing(body []byte) (string, error) {
	var listing struct {
		Items []struct {
			URL string `json:"url"`
		} `json:"items"`
		TotalCount *int `json:"totalCount"`
	}

	if err := json.Unmarshal(body, &listing); err != nil {
		return "", fmt.Errorf("GET /images: response is not valid JSON: %w", err)
	}

	// totalCount is the envelope field a client reads to page, so requiring it
	// is what distinguishes this endpoint's shape from an arbitrary object that
	// happens to have an array in it.
	if listing.TotalCount == nil {
		return "", fmt.Errorf("GET /images: response has no totalCount, so it is not an image listing")
	}

	if len(listing.Items) == 0 {
		return "", errEmptyCatalogue
	}
	if listing.Items[0].URL == "" {
		return "", fmt.Errorf("GET /images: the first entry carries no url")
	}
	return listing.Items[0].URL, nil
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
		err := fmt.Errorf("HEAD %s: %s", imageURL, resp.Status)
		if resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusTooManyRequests {
			return refusedError{err: err}
		}
		return err
	}
	return nil
}

// statusError is an HTTP response the probe cannot use.
//
// It is a type rather than a formatted string because Probe has to tell it apart
// from a transport failure: a 404 on /version means the instance is answering and
// simply does not implement the handshake, which leaves every other check worth
// making. A dial error means there is nothing left to ask.
type statusError struct {
	Status string
	Code   int
}

func (e statusError) Error() string { return e.Status }

// refused reports whether the status is the host declining to answer this
// checker rather than an answer about the instance.
//
// 401, 403 and 429 are the three ways a server says "not you": a firewall, a bot
// rule, or a rate limit aimed at the prober's address. None of them is evidence
// that the catalogue is broken, which is why they are not published as failure.
func (e statusError) refused() bool {
	return e.Code == http.StatusUnauthorized ||
		e.Code == http.StatusForbidden ||
		e.Code == http.StatusTooManyRequests
}

// refusedError marks the image host refusing the HEAD, so Probe can record a
// refusal rather than a dead image.
type refusedError struct {
	err error
}

func (e refusedError) Error() string { return e.err.Error() }
func (e refusedError) Unwrap() error { return e.err }

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
		return nil, statusError{Status: resp.Status, Code: resp.StatusCode}
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
