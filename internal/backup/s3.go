package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 is a bucket on S3-compatible storage. Only what backups need is
// spoken: put, get, list and delete, signed with AWS Signature Version 4.
type S3 struct {
	// Endpoint is the service's address, such as https://s3.us-east-1.amazonaws.com,
	// https://s3.us-west-004.backblazeb2.com or https://<account>.r2.cloudflarestorage.com.
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"` // such as us-east-1; "auto" for R2
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix,omitempty"` // a folder in the bucket, such as boxoffice/
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	// PathStyle puts the bucket in the path (endpoint/bucket/key) rather
	// than the host name; MinIO usually needs it.
	PathStyle bool `json:"pathStyle,omitempty"`
}

func (s S3) Check() error {
	u, err := url.Parse(s.Endpoint)
	switch {
	case err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "":
		return errors.New("enter the storage's endpoint, such as https://s3.us-east-1.amazonaws.com")
	case s.Bucket == "":
		return errors.New("enter the bucket")
	case s.AccessKey == "" || s.SecretKey == "":
		return errors.New("enter the access key and secret")
	}
	return nil
}

type s3Store struct {
	S3
	base   *url.URL
	client *http.Client
	now    func() time.Time
}

func newS3(s S3) (*s3Store, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	if s.Prefix != "" && !strings.HasSuffix(s.Prefix, "/") {
		s.Prefix += "/"
	}
	s.Prefix = strings.TrimLeft(s.Prefix, "/")
	return &s3Store{S3: s, base: u, client: &http.Client{Timeout: 10 * time.Minute}, now: time.Now}, nil
}

// object is the address of a key in the bucket.
func (s *s3Store) object(key string, q url.Values) *url.URL {
	u := *s.base
	if s.PathStyle {
		u.Path = "/" + s.Bucket + "/" + key
	} else {
		u.Host = s.Bucket + "." + u.Host
		u.Path = "/" + key
	}
	if q != nil {
		u.RawQuery = encodeQuery(q)
	}
	return &u
}

func encodeQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode is AWS's encoding: everything but unreserved characters, and
// slashes too unless they separate a path.
func uriEncode(s string, slash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !slash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacSHA(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

const unsigned = "UNSIGNED-PAYLOAD"

// sign adds AWS Signature Version 4 headers to a request.
func (s *s3Store) sign(req *http.Request, payloadHash string) {
	t := s.now().UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	canonHeaders := "host:" + req.URL.Host + "\nx-amz-content-sha256:" + payloadHash + "\nx-amz-date:" + amzDate + "\n"
	canon := strings.Join([]string{req.Method, uriEncode(req.URL.Path, false), req.URL.RawQuery, canonHeaders, strings.Join(signed, ";"), payloadHash}, "\n")
	h := sha256.Sum256([]byte(canon))
	scope := day + "/" + s.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	k := hmacSHA([]byte("AWS4"+s.SecretKey), day)
	k = hmacSHA(k, s.Region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/"+scope+", SignedHeaders="+strings.Join(signed, ";")+", Signature="+sig)
}

func (s *s3Store) do(ctx context.Context, method string, u *url.URL, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	// A body is sent unhashed, which every S3-compatible service accepts;
	// requests without one sign the empty hash.
	hash := emptyHash
	if body != nil {
		hash = unsigned
	}
	s.sign(req, hash)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		var e struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		if xml.Unmarshal(b, &e) == nil && e.Code != "" {
			return nil, fmt.Errorf("%s: %s", e.Code, e.Message)
		}
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res, nil
}

var emptyHash = func() string { h := sha256.Sum256(nil); return hex.EncodeToString(h[:]) }()

func (s *s3Store) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	res, err := s.do(ctx, "PUT", s.object(s.Prefix+name, nil), r, size)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (s *s3Store) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	res, err := s.do(ctx, "GET", s.object(s.Prefix+name, nil), nil, 0)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (s *s3Store) Delete(ctx context.Context, name string) error {
	res, err := s.do(ctx, "DELETE", s.object(s.Prefix+name, nil), nil, 0)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (s *s3Store) List(ctx context.Context) ([]string, error) {
	var out []string
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {s.Prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u := s.object("", q)
		if s.PathStyle {
			u.Path = "/" + s.Bucket
		}
		res, err := s.do(ctx, "GET", u, nil, 0)
		if err != nil {
			return nil, err
		}
		var page struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			IsTruncated bool   `xml:"IsTruncated"`
			Next        string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&page)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, c := range page.Contents {
			if name := strings.TrimPrefix(c.Key, s.Prefix); name != "" && !strings.Contains(name, "/") {
				out = append(out, name)
			}
		}
		if !page.IsTruncated || page.Next == "" {
			return out, nil
		}
		token = page.Next
	}
}
