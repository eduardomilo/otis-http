package httpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/otis-http/otis/internal/resolve"
)

// AWSCredentials resolves and caches credentials for "@auth aws". Profile
// lookups go through the AWS SDK default chain (environment, shared
// config/credentials files, SSO, assume-role, credential_process, ...), so
// whatever works for the aws CLI works here. Results are cached per profile
// for the life of the session; temporary credentials refresh themselves.
//
// Reaching into the user's AWS credentials is a machine-level privilege. It
// is fine for the GUI and CLI run by the user; an MCP surface must gate it
// behind explicit consent (see docs/FORMAT.md §3.3).
type AWSCredentials struct {
	mu       sync.Mutex
	profiles map[string]awsProfile
	// loadConfig is replaceable in tests.
	loadConfig func(ctx context.Context, profile string) (aws.Config, error)
}

type awsProfile struct {
	creds  aws.CredentialsProvider
	region string
}

// NewAWSCredentials returns an empty cache backed by the SDK default chain.
func NewAWSCredentials() *AWSCredentials {
	return &AWSCredentials{profiles: map[string]awsProfile{}, loadConfig: loadAWSConfig}
}

func loadAWSConfig(ctx context.Context, profile string) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

// forProfile returns the credentials provider and default region for a
// profile ("" = default chain).
func (c *AWSCredentials) forProfile(ctx context.Context, profile string) (awsProfile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.profiles[profile]; ok {
		return p, nil
	}
	cfg, err := c.loadConfig(ctx, profile)
	if err != nil {
		return awsProfile{}, err
	}
	p := awsProfile{creds: aws.NewCredentialsCache(cfg.Credentials), region: cfg.Region}
	c.profiles[profile] = p
	return p, nil
}

// awsRegionRe matches an AWS region label such as us-east-1 or us-gov-west-1.
var awsRegionRe = regexp.MustCompile(`^[a-z]{2}(-gov|-iso[a-z]?)?-[a-z]+-\d$`)

// deriveAWSEndpoint guesses service and region from an amazonaws.com host:
// "<x>.<service>.<region>.amazonaws.com" or "<service>.amazonaws.com"
// (global/legacy endpoints, region us-east-1). It returns empty strings when
// the host is not an AWS endpoint.
func deriveAWSEndpoint(host string) (service, region string) {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	labels := strings.Split(host, ".")
	idx := -1
	for i, l := range labels {
		if l == "amazonaws" {
			idx = i
			break
		}
	}
	if idx < 1 {
		return "", ""
	}
	before := labels[:idx]
	last := before[len(before)-1]
	if awsRegionRe.MatchString(last) {
		if len(before) < 2 {
			return "", last
		}
		return before[len(before)-2], last
	}
	return last, "us-east-1"
}

// signAWS resolves credentials for auth and signs req in place (headers
// Authorization, X-Amz-Date, X-Amz-Content-Sha256 and, for temporary
// credentials, X-Amz-Security-Token). Error messages never carry key
// material.
func signAWS(ctx context.Context, req *Request, auth *resolve.Auth, creds *AWSCredentials, now time.Time) error {
	var provider aws.CredentialsProvider
	region := auth.Region
	if auth.AccessKey != "" {
		provider = credentials.NewStaticCredentialsProvider(auth.AccessKey, auth.SecretKey, auth.SessionToken)
	} else {
		if creds == nil {
			return fmt.Errorf("@auth aws: no AWS credential resolver configured")
		}
		p, err := creds.forProfile(ctx, auth.Profile)
		if err != nil {
			return fmt.Errorf("@auth aws: load %s: %w", describeProfile(auth.Profile), err)
		}
		provider = p.creds
		if region == "" {
			region = p.region
		}
	}
	cr, err := provider.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("@auth aws: credentials for %s: %w", describeProfile(auth.Profile), err)
	}

	hreq, err := http.NewRequest(req.Method, req.URL, nil)
	if err != nil {
		return fmt.Errorf("@auth aws: %w", err)
	}
	for _, h := range req.Headers {
		if strings.EqualFold(h.Name, "Host") {
			hreq.Host = h.Value
			continue
		}
		hreq.Header.Add(h.Name, h.Value)
	}
	service := auth.Service
	if service == "" || region == "" {
		ds, dr := deriveAWSEndpoint(hreq.Host)
		if service == "" {
			service = ds
		}
		if region == "" {
			region = dr
		}
	}
	if service == "" {
		return fmt.Errorf("@auth aws: cannot derive the service from host %q; add service=<name>", hreq.Host)
	}
	if region == "" {
		return fmt.Errorf("@auth aws: cannot derive the region from host %q or %s; add region=<name>", hreq.Host, describeProfile(auth.Profile))
	}

	sum := sha256.Sum256(req.Body)
	payloadHash := hex.EncodeToString(sum[:])
	hreq.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signer := v4.NewSigner()
	if err := signer.SignHTTP(ctx, cr, hreq, payloadHash, service, region, now.UTC()); err != nil {
		return fmt.Errorf("@auth aws: sign: %w", err)
	}
	for _, name := range []string{"Authorization", "X-Amz-Date", "X-Amz-Content-Sha256", "X-Amz-Security-Token"} {
		if v := hreq.Header.Get(name); v != "" {
			req.Headers = append(req.Headers, Header{Name: name, Value: v})
		}
	}
	return nil
}

func describeProfile(profile string) string {
	if profile == "" {
		return "the default AWS credential chain"
	}
	return fmt.Sprintf("AWS profile %q", profile)
}
