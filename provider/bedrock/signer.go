package bedrock

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	serviceName = "bedrock"
	algorithm   = "AWS4-HMAC-SHA256"
)

// signRequest signs an http.Request with AWS SigV4 for the Bedrock service.
func signRequest(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region string) error {
	t := time.Now().UTC()
	dateStamp := t.Format("20060102")
	amzDate := t.Format("20060102T150405Z")

	// Canonical request
	canonicalHeaders, signedHeaders := canonicalHeaderString(req, amzDate, sessionToken)
	payloadHash := sha256Hex(body)
	canonicalReq := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		canonicalURI(req.URL.Path),
		canonicalQuery(req.URL.RawQuery),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	)

	// String to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, serviceName)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		algorithm,
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalReq)),
	)

	// Signing key
	signingKey := deriveSigningKey(secretKey, dateStamp, region, serviceName)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Authorization header
	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, accessKey, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Date", amzDate)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}
	req.Header.Set("Content-Type", "application/json")

	return nil
}

func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func canonicalQuery(query string) string {
	if query == "" {
		return ""
	}
	parts := strings.Split(query, "&")
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

func canonicalHeaderString(req *http.Request, amzDate, sessionToken string) (string, string) {
	h := req.Header.Clone()
	h.Set("Host", req.Host)
	h.Set("X-Amz-Date", amzDate)
	if sessionToken != "" {
		h.Set("X-Amz-Security-Token", sessionToken)
	}
	delete(h, "Authorization")

	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(strings.Join(h[http.CanonicalHeaderKey(k)], ","))
		b.WriteString("\n")
	}
	return b.String(), strings.Join(keys, ";")
}

func deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// signedDo sends a request with SigV4 signing applied.
func signedDo(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region string, client *http.Client) (*http.Response, error) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	if err := signRequest(req, body, accessKey, secretKey, sessionToken, region); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	return resp, nil
}
