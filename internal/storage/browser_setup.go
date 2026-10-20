package storage

import (
	"context"
	"crypto/md5" // S3's Content-MD5 transport checksum, not authentication.
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// ErrBrowserCORS distinguishes a rejected preflight from a network failure.
var ErrBrowserCORS = fmt.Errorf("%w: browser access is not configured", ErrInvalidConfig)

func validBrowserOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" &&
		(u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) && !strings.Contains(origin, "*")
}

// EnsureBrowser changes CORS only when a preflight proves it is missing. It
// reads and merges the current document; a read failure must never erase rules.
// The returned flag means the policy is installed but may still be propagating.
func (s *S3) EnsureBrowser(ctx context.Context, origin string) (bool, error) {
	if err := s.CheckBrowser(ctx, origin); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrBrowserCORS) {
		return false, err
	}
	result, err := s.client.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: aws.String(s.config.Bucket)})
	var rules []types.CORSRule
	if err == nil {
		rules = result.CORSRules
	} else {
		var api smithy.APIError
		if !errors.As(err, &api) || api.ErrorCode() != "NoSuchCORSConfiguration" {
			return false, fmt.Errorf("%w: Gateway could not read bucket CORS. Allow this key to read and update bucket CORS (%s), then save again. Existing rules were not changed", ErrInvalidConfig, s.corsPermissions())
		}
	}
	merged, changed, err := mergeBrowserRule(rules, origin)
	if err != nil {
		return false, err
	}
	if changed {
		_, err = s.client.PutBucketCors(ctx, &s3.PutBucketCorsInput{Bucket: aws.String(s.config.Bucket), CORSConfiguration: &types.CORSConfiguration{CORSRules: merged}}, func(o *s3.Options) {
			// R2/B2 accept Content-MD5 but do not consistently support the SDK's
			// default flexible CRC32 checksum for this bucket operation.
			o.APIOptions = append(o.APIOptions, corsContentMD5)
		})
		if err != nil {
			return false, fmt.Errorf("%w: Gateway could not update bucket CORS. Allow this key to update bucket CORS (%s), then save again", ErrInvalidConfig, s.corsPermissions())
		}
	}
	// Verify the persisted document before activating the connection. Do not wait
	// inside a settings request for the provider's browser-facing caches to expire.
	verified, err := s.client.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: aws.String(s.config.Bucket)})
	if err != nil || !sameCORSRules(verified.CORSRules, merged) {
		return false, fmt.Errorf("%w: bucket CORS update could not be confirmed; test the connection and save again", ErrInvalidConfig)
	}
	return true, nil
}

// Providers can reorder method/header lists or normalize header case. Rule
// order, destinations and other settings must still survive the round trip.
func sameCORSRules(a, b []types.CORSRule) bool {
	normalize := func(rules []types.CORSRule) []types.CORSRule {
		out := append([]types.CORSRule(nil), rules...)
		values := func(input []string, fold bool) []string {
			result := append([]string(nil), input...)
			for i := range result {
				if fold {
					result[i] = strings.ToLower(result[i])
				}
			}
			sort.Strings(result)
			return result
		}
		for i := range out {
			out[i].AllowedMethods = values(out[i].AllowedMethods, false)
			out[i].AllowedOrigins = values(out[i].AllowedOrigins, false)
			out[i].AllowedHeaders = values(out[i].AllowedHeaders, true)
			out[i].ExposeHeaders = values(out[i].ExposeHeaders, true)
		}
		return out
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}

func (s *S3) corsPermissions() string {
	host := ""
	if u, err := url.Parse(s.config.Endpoint); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	switch {
	case strings.HasSuffix(host, ".backblazeb2.com"):
		return "Backblaze B2 key capabilities: readBuckets and writeBuckets"
	case strings.HasSuffix(host, ".r2.cloudflarestorage.com"):
		return "Cloudflare R2 token permission: Admin Read & Write"
	default:
		return "bucket permissions: s3:GetBucketCORS and s3:PutBucketCORS (or your provider's equivalents)"
	}
}

func mergeBrowserRule(rules []types.CORSRule, origin string) ([]types.CORSRule, bool, error) {
	sum := sha256.Sum256([]byte(origin))
	id := fmt.Sprintf("gateway-%x", sum[:12])
	rule := types.CORSRule{ID: &id, AllowedOrigins: []string{origin}, AllowedMethods: []string{"PUT", "GET", "HEAD"}, AllowedHeaders: []string{"content-type", "authorization", "x-amz-*", "range"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: aws.Int32(3600)}
	merged := append([]types.CORSRule(nil), rules...)
	for i, existing := range rules {
		if aws.ToString(existing.ID) != id {
			continue
		}
		if len(existing.AllowedOrigins) != 1 || existing.AllowedOrigins[0] != origin {
			return nil, false, fmt.Errorf("%w: the Gateway CORS rule ID is already used by a different rule; existing rules were not changed", ErrInvalidConfig)
		}
		appendMissing := func(have, need []string) []string {
			out := append([]string(nil), have...)
			for _, v := range need {
				found := false
				for _, h := range out {
					if strings.EqualFold(h, v) {
						found = true
						break
					}
				}
				if !found {
					out = append(out, v)
				}
			}
			return out
		}
		existing.AllowedMethods = appendMissing(existing.AllowedMethods, rule.AllowedMethods)
		existing.AllowedHeaders = appendMissing(existing.AllowedHeaders, rule.AllowedHeaders)
		existing.ExposeHeaders = appendMissing(existing.ExposeHeaders, rule.ExposeHeaders)
		merged[i] = existing
		return merged, !reflect.DeepEqual(merged, rules), nil
	}
	if len(rules) >= 100 {
		return nil, false, fmt.Errorf("%w: bucket already has 100 CORS rules; remove an unused rule before saving", ErrInvalidConfig)
	}
	return append(merged, rule), true, nil
}

func corsContentMD5(stack *middleware.Stack) error {
	if _, err := stack.Initialize.Remove("AWSChecksum:SetupInputContext"); err != nil {
		return err
	}
	return stack.Build.Add(middleware.BuildMiddlewareFunc("GatewayCORSContentMD5", func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
		req, ok := in.Request.(*smithyhttp.Request)
		if !ok {
			return middleware.BuildOutput{}, middleware.Metadata{}, errors.New("unexpected CORS request type")
		}
		hash := md5.New()
		if _, err := io.Copy(hash, req.GetStream()); err != nil {
			return middleware.BuildOutput{}, middleware.Metadata{}, err
		}
		if err := req.RewindStream(); err != nil {
			return middleware.BuildOutput{}, middleware.Metadata{}, err
		}
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(hash.Sum(nil)))
		return next.HandleBuild(ctx, in)
	}), middleware.After)
}
