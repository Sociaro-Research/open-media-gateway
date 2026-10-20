package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// CheckBrowser checks the preflight required by reference uploads. Ordinary
// server-side read/write probes cannot detect missing bucket CORS. Headless
// clients without an Origin retain the server-only connection check.
func (s *S3) CheckBrowser(ctx context.Context, origin string) error {
	if origin == "" {
		return nil
	}
	if !validBrowserOrigin(origin) {
		return fmt.Errorf("%w: browser origin must be an HTTPS origin (HTTP is allowed only on localhost)", ErrInvalidConfig)
	}
	key := path.Join(s.config.Prefix, ".gateway-check", uuid.NewString())
	target, err := s3.NewPresignClient(s.client).PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.config.Bucket), Key: &key,
	}, s3.WithPresignExpires(time.Minute))
	if err != nil {
		return fmt.Errorf("%w: could not prepare the browser upload check", ErrInvalidConfig)
	}
	for _, method := range []string{"PUT", "GET", "HEAD"} {
		if err := s.checkBrowserMethod(ctx, origin, target.URL, method); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3) checkBrowserMethod(ctx context.Context, origin, targetURL, method string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, targetURL, nil)
	if err != nil {
		return fmt.Errorf("%w: could not prepare the browser upload check", ErrInvalidConfig)
	}
	req.Header.Set("User-Agent", "Media-Gateway-Storage-CORS-Check/1.0")
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	headers := "content-type,x-amz-content-sha256"
	if strings.HasSuffix(strings.ToLower(req.URL.Hostname()), ".backblazeb2.com") {
		headers += ",authorization,x-amz-date"
	}
	if method != "PUT" {
		headers = "range"
	}
	req.Header.Set("Access-Control-Request-Headers", headers)
	client := *s.sourceClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: could not reach storage for the browser upload check", ErrInvalidConfig)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	allowedOrigin := response.Header.Get("Access-Control-Allow-Origin")
	allowedHeaders := response.Header.Get("Access-Control-Allow-Headers")
	if response.StatusCode >= 300 && response.StatusCode != 400 && response.StatusCode != 403 && response.StatusCode != 405 {
		return fmt.Errorf("%w: storage returned an unexpected status during the browser access check", ErrInvalidConfig)
	}
	valid := response.StatusCode >= 200 && response.StatusCode < 300 &&
		(allowedOrigin == origin || allowedOrigin == "*") && corsValue(response.Header.Get("Access-Control-Allow-Methods"), method, false)
	for _, h := range strings.Split(headers, ",") {
		if h == "authorization" {
			valid = valid && corsAuthorization(allowedHeaders)
		} else {
			valid = valid && corsValue(allowedHeaders, h, true)
		}
	}
	if !valid {
		return fmt.Errorf("%w: storage is reachable, but bucket CORS does not allow %s from %s. Save the connection to let Gateway configure browser access", ErrBrowserCORS, method, origin)
	}

	return nil
}

func corsValue(list, wanted string, header bool) bool {
	for _, value := range strings.Split(list, ",") {
		value = strings.TrimSpace(value)
		if value == wanted || (header && (value == "*" || strings.EqualFold(value, wanted))) {
			return true
		}
	}
	return false
}

func corsAuthorization(list string) bool {
	for _, value := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(value), "authorization") {
			return true
		}
	}
	return false
}
