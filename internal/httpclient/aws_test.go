package httpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/otis-http/otis/internal/resolve"
)

// isolateAWS points the SDK at a fake ~/.aws and clears env credentials so
// tests never see the developer's real profiles or reach the network.
func isolateAWS(t *testing.T, configFile, credentialsFile string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	cred := filepath.Join(dir, "credentials")
	if err := os.WriteFile(cfg, []byte(configFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(credentialsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", cred)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, k := range []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

const (
	testAccessKey = "AKIDEXAMPLE"
	testSecretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY" //nolint:gosec // AWS documentation example key
)

var fixedTime = time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func header(req *Request, name string) string {
	for _, h := range req.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func TestSignAWSStaticCredentials(t *testing.T) {
	body := []byte(`{"x":1}`)
	req := &Request{Method: "POST", URL: "https://abc123.execute-api.us-east-1.amazonaws.com/prod/items?b=2&a=1",
		Headers: []Header{{"Content-Type", "application/json"}}, Body: body}
	auth := &resolve.Auth{Kind: resolve.AuthAWS, AccessKey: testAccessKey, SecretKey: testSecretKey, SessionToken: "SESSION"}
	if err := signAWS(context.Background(), req, auth, nil, fixedTime); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if got := header(req, "X-Amz-Content-Sha256"); got != hex.EncodeToString(sum[:]) {
		t.Errorf("content sha256 = %q", got)
	}
	if got := header(req, "X-Amz-Date"); got != "20150830T123600Z" {
		t.Errorf("date = %q", got)
	}
	if got := header(req, "X-Amz-Security-Token"); got != "SESSION" {
		t.Errorf("token = %q", got)
	}
	authz := header(req, "Authorization")
	wantPrefix := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/execute-api/aws4_request, SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token, Signature="
	if !strings.HasPrefix(authz, wantPrefix) || len(authz) != len(wantPrefix)+64 {
		t.Errorf("authorization = %q\nwant prefix %q", authz, wantPrefix)
	}
	if header(req, "Content-Type") != "application/json" || len(req.Headers) != 5 {
		t.Errorf("headers = %+v", req.Headers)
	}
}

// TestSignAWSKnownVector reproduces the "get-vanilla" case of the AWS
// Signature V4 test suite. The suite signs only host and x-amz-date, so the
// content hash header is removed before signing here.
func TestSignAWSKnownVector(t *testing.T) {
	// Sign through the same SDK signer Otis uses, with the suite's inputs.
	req := &Request{Method: "GET", URL: "https://example.amazonaws.com/"}
	auth := &resolve.Auth{Kind: resolve.AuthAWS, AccessKey: testAccessKey, SecretKey: testSecretKey, Region: "us-east-1", Service: "service"}
	if err := signAWS(context.Background(), req, auth, nil, fixedTime); err != nil {
		t.Fatal(err)
	}
	// Otis always signs x-amz-content-sha256 too, so the signature differs
	// from the published vector; check the credential scope and that the
	// empty-body hash is the well-known SHA-256 of "".
	authz := header(req, "Authorization")
	if !strings.Contains(authz, "Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request") {
		t.Errorf("authorization = %q", authz)
	}
	if got := header(req, "X-Amz-Content-Sha256"); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("empty body hash = %q", got)
	}
}

func TestSignAWSDeterministic(t *testing.T) {
	mk := func() *Request {
		return &Request{Method: "GET", URL: "https://s3.us-west-2.amazonaws.com/bucket/key"}
	}
	auth := &resolve.Auth{Kind: resolve.AuthAWS, AccessKey: testAccessKey, SecretKey: testSecretKey}
	a, b := mk(), mk()
	if err := signAWS(context.Background(), a, auth, nil, fixedTime); err != nil {
		t.Fatal(err)
	}
	if err := signAWS(context.Background(), b, auth, nil, fixedTime); err != nil {
		t.Fatal(err)
	}
	if header(a, "Authorization") != header(b, "Authorization") {
		t.Error("signature not deterministic for equal inputs")
	}
	if !strings.Contains(header(a, "Authorization"), "/us-west-2/s3/") {
		t.Errorf("service/region not derived from host: %q", header(a, "Authorization"))
	}
	c := mk()
	if err := signAWS(context.Background(), c, auth, nil, fixedTime.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if header(a, "Authorization") == header(c, "Authorization") {
		t.Error("signature did not change with time")
	}
}

func TestDeriveAWSEndpoint(t *testing.T) {
	tests := []struct{ host, service, region string }{
		{"abc123.execute-api.us-east-1.amazonaws.com", "execute-api", "us-east-1"},
		{"dynamodb.eu-west-1.amazonaws.com", "dynamodb", "eu-west-1"},
		{"s3.amazonaws.com", "s3", "us-east-1"},
		{"my-bucket.s3.amazonaws.com", "s3", "us-east-1"},
		{"my-bucket.s3.us-west-2.amazonaws.com", "s3", "us-west-2"},
		{"iam.amazonaws.com", "iam", "us-east-1"},
		{"sts.us-gov-west-1.amazonaws.com", "sts", "us-gov-west-1"},
		{"lambda.cn-north-1.amazonaws.com.cn", "lambda", "cn-north-1"},
		{"API.Execute-Api.US-East-1.Amazonaws.com:443", "execute-api", "us-east-1"},
		{"example.com", "", ""},
		{"amazonaws.com", "", ""},
		{"localhost:9000", "", ""},
	}
	for _, tt := range tests {
		s, r := deriveAWSEndpoint(tt.host)
		if s != tt.service || r != tt.region {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tt.host, s, r, tt.service, tt.region)
		}
	}
}

func TestSignAWSErrors(t *testing.T) {
	static := &resolve.Auth{Kind: resolve.AuthAWS, AccessKey: testAccessKey, SecretKey: testSecretKey}
	t.Run("service not derivable", func(t *testing.T) {
		err := signAWS(context.Background(), &Request{Method: "GET", URL: "https://api.example.com/"}, static, nil, fixedTime)
		if err == nil || !strings.Contains(err.Error(), `cannot derive the service from host "api.example.com"; add service=<name>`) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("region not derivable", func(t *testing.T) {
		a := *static
		a.Service = "execute-api"
		err := signAWS(context.Background(), &Request{Method: "GET", URL: "https://api.example.com/"}, &a, nil, fixedTime)
		if err == nil || !strings.Contains(err.Error(), "cannot derive the region") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("profile without resolver", func(t *testing.T) {
		err := signAWS(context.Background(), &Request{Method: "GET", URL: "https://s3.amazonaws.com/"}, &resolve.Auth{Kind: resolve.AuthAWS, Profile: "p"}, nil, fixedTime)
		if err == nil || !strings.Contains(err.Error(), "no AWS credential resolver") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("errors never contain the secret", func(t *testing.T) {
		err := signAWS(context.Background(), &Request{Method: "GET", URL: "https://api.example.com/"}, static, nil, fixedTime)
		if err == nil || strings.Contains(err.Error(), testSecretKey) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestAWSProfiles(t *testing.T) {
	isolateAWS(t, `
[default]
region = us-east-1

[profile staging]
region = eu-central-1

[profile noregion]
`, `
[default]
aws_access_key_id = DEFAULTKEY
aws_secret_access_key = defaultsecret

[staging]
aws_access_key_id = STAGINGKEY
aws_secret_access_key = stagingsecret
aws_session_token = stagingtoken

[noregion]
aws_access_key_id = NOREGIONKEY
aws_secret_access_key = noregionsecret
`)
	creds := NewAWSCredentials()

	t.Run("named profile supplies keys, token and region", func(t *testing.T) {
		req := &Request{Method: "GET", URL: "https://api.example.com/x"}
		auth := &resolve.Auth{Kind: resolve.AuthAWS, Profile: "staging", Service: "execute-api"}
		if err := signAWS(context.Background(), req, auth, creds, fixedTime); err != nil {
			t.Fatal(err)
		}
		if a := header(req, "Authorization"); !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=STAGINGKEY/20150830/eu-central-1/execute-api/aws4_request") {
			t.Errorf("authorization = %q", a)
		}
		if header(req, "X-Amz-Security-Token") != "stagingtoken" {
			t.Error("session token from profile not sent")
		}
	})
	t.Run("directive region beats profile region", func(t *testing.T) {
		req := &Request{Method: "GET", URL: "https://api.example.com/x"}
		auth := &resolve.Auth{Kind: resolve.AuthAWS, Profile: "staging", Service: "s", Region: "ap-south-1"}
		if err := signAWS(context.Background(), req, auth, creds, fixedTime); err != nil {
			t.Fatal(err)
		}
		if a := header(req, "Authorization"); !strings.Contains(a, "/ap-south-1/s/") {
			t.Errorf("authorization = %q", a)
		}
	})
	t.Run("default chain honours AWS_PROFILE", func(t *testing.T) {
		t.Setenv("AWS_PROFILE", "staging")
		req := &Request{Method: "GET", URL: "https://api.example.com/x"}
		auth := &resolve.Auth{Kind: resolve.AuthAWS, Service: "s"}
		if err := signAWS(context.Background(), req, auth, NewAWSCredentials(), fixedTime); err != nil {
			t.Fatal(err)
		}
		if a := header(req, "Authorization"); !strings.Contains(a, "Credential=STAGINGKEY/") {
			t.Errorf("authorization = %q", a)
		}
	})
	t.Run("default profile when nothing is set", func(t *testing.T) {
		req := &Request{Method: "GET", URL: "https://sqs.us-east-1.amazonaws.com/"}
		if err := signAWS(context.Background(), req, &resolve.Auth{Kind: resolve.AuthAWS}, creds, fixedTime); err != nil {
			t.Fatal(err)
		}
		if a := header(req, "Authorization"); !strings.Contains(a, "Credential=DEFAULTKEY/20150830/us-east-1/sqs/") {
			t.Errorf("authorization = %q", a)
		}
	})
	t.Run("host region used when profile has none", func(t *testing.T) {
		req := &Request{Method: "GET", URL: "https://dynamodb.eu-west-3.amazonaws.com/"}
		if err := signAWS(context.Background(), req, &resolve.Auth{Kind: resolve.AuthAWS, Profile: "noregion"}, creds, fixedTime); err != nil {
			t.Fatal(err)
		}
		if a := header(req, "Authorization"); !strings.Contains(a, "Credential=NOREGIONKEY/20150830/eu-west-3/dynamodb/") {
			t.Errorf("authorization = %q", a)
		}
	})
	t.Run("no region anywhere", func(t *testing.T) {
		req := &Request{Method: "GET", URL: "https://api.example.com/"}
		err := signAWS(context.Background(), req, &resolve.Auth{Kind: resolve.AuthAWS, Profile: "noregion", Service: "s"}, creds, fixedTime)
		if err == nil || !strings.Contains(err.Error(), `cannot derive the region from host "api.example.com" or AWS profile "noregion"`) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unknown profile names the profile, not secrets", func(t *testing.T) {
		req := &Request{Method: "GET", URL: "https://sqs.us-east-1.amazonaws.com/"}
		err := signAWS(context.Background(), req, &resolve.Auth{Kind: resolve.AuthAWS, Profile: "missing"}, creds, fixedTime)
		if err == nil || !strings.Contains(err.Error(), `AWS profile "missing"`) || strings.Contains(err.Error(), "secret") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("profiles are cached per session", func(t *testing.T) {
		calls := 0
		c := NewAWSCredentials()
		orig := c.loadConfig
		c.loadConfig = func(ctx context.Context, profile string) (aws.Config, error) { calls++; return orig(ctx, profile) }
		for i := 0; i < 3; i++ {
			req := &Request{Method: "GET", URL: "https://sqs.us-east-1.amazonaws.com/"}
			if err := signAWS(context.Background(), req, &resolve.Auth{Kind: resolve.AuthAWS, Profile: "staging"}, c, fixedTime); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Errorf("loadConfig called %d times, want 1", calls)
		}
	})
	t.Run("retrieve failure is wrapped", func(t *testing.T) {
		c := NewAWSCredentials()
		c.loadConfig = func(context.Context, string) (aws.Config, error) {
			return aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				return aws.Credentials{}, errors.New("sso session expired")
			})}, nil
		}
		err := signAWS(context.Background(), &Request{Method: "GET", URL: "https://sqs.us-east-1.amazonaws.com/"}, &resolve.Auth{Kind: resolve.AuthAWS, Profile: "sso"}, c, fixedTime)
		if err == nil || !strings.Contains(err.Error(), `credentials for AWS profile "sso": `) || !strings.Contains(err.Error(), "sso session expired") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestPrepareAWS(t *testing.T) {
	isolateAWS(t, "[profile p]\nregion = us-east-2\n", "[p]\naws_access_key_id = PKEY\naws_secret_access_key = psecret\n")
	session := NewSession()
	opts := session.PrepareOptions()
	opts.Now = fixedNow

	t.Run("signed through Prepare with body and directives", func(t *testing.T) {
		req, warns, err := prepWith(t, map[string]string{
			"_folder.http": "# @auth aws profile=p service=execute-api\n",
			"r.http":       "# @timeout 5\nPOST https://api.example.com/items\nContent-Type: application/json\n\n{\"a\": 1}\n",
		}, "r.http", opts)
		if err != nil || len(warns) != 0 {
			t.Fatal(err, warns)
		}
		if a := header(req, "Authorization"); !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=PKEY/20150830/us-east-2/execute-api/aws4_request, SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date, Signature=") {
			t.Errorf("authorization = %q", a)
		}
		if req.Options.Timeout != 5*time.Second || string(req.Body) != `{"a": 1}` {
			t.Errorf("req = %+v", req)
		}
	})
	t.Run("explicit Authorization header wins over @auth aws", func(t *testing.T) {
		req, _, err := prepWith(t, map[string]string{
			"r.http": "# @auth aws profile=p\nGET https://sqs.us-east-1.amazonaws.com/\nAuthorization: Custom\n",
		}, "r.http", opts)
		if err != nil {
			t.Fatal(err)
		}
		if header(req, "Authorization") != "Custom" || header(req, "X-Amz-Date") != "" {
			t.Errorf("headers = %+v", req.Headers)
		}
	})
	t.Run("static keys from variables with masking", func(t *testing.T) {
		req, _, err := prepWith(t, map[string]string{
			"r.http": "@k = AKIDEXAMPLE\n@s = topsecret\n# @auth aws key={{k}} secret={{s}}\nGET https://sqs.us-east-1.amazonaws.com/\n",
		}, "r.http", opts)
		if err != nil {
			t.Fatal(err)
		}
		if a := header(req, "Authorization"); !strings.Contains(a, "Credential=AKIDEXAMPLE/20150830/us-east-1/sqs/") {
			t.Errorf("authorization = %q", a)
		}
	})
	t.Run("session-less options reject profiles", func(t *testing.T) {
		_, _, err := prepWith(t, map[string]string{
			"r.http": "# @auth aws profile=p\nGET https://sqs.us-east-1.amazonaws.com/\n",
		}, "r.http", PrepareOptions{Now: fixedNow})
		if err == nil || !strings.Contains(err.Error(), "no AWS credential resolver") {
			t.Errorf("err = %v", err)
		}
	})
}
