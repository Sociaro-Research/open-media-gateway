package alibaba

import "github.com/Sociaro-Research/open-media-gateway/internal/providerkit"

// Exact reviewed native codes only; unknown codes and provider prose remain private.
// https://help.aliyun.com/en/model-studio/error-code
var failurePolicy = providerkit.NewFailurePolicy("Alibaba Model Studio", map[string]providerkit.FailureKind{
	"CLIENT_ERROR":               providerkit.FailureInput,
	"InvalidParameter":           providerkit.FailureInput,
	"invalid_parameter_error":    providerkit.FailureInput,
	"InvalidApiKey":              providerkit.FailureCredentials,
	"invalid_api_key":            providerkit.FailureCredentials,
	"AccessDenied":               providerkit.FailurePermission,
	"access_denied":              providerkit.FailurePermission,
	"ModelNotFound":              providerkit.FailureConfiguration,
	"NotFound":                   providerkit.FailureConfiguration,
	"Throttling.RateQuota":       providerkit.FailureRateLimit,
	"Throttling.AllocationQuota": providerkit.FailureQuota,
	"insufficient_quota":         providerkit.FailureQuota,
	"Arrearage":                  providerkit.FailureBalance,
	"DataInspectionFailed":       providerkit.FailureContentPolicy,
	"data_inspection_failed":     providerkit.FailureContentPolicy,
	"InternalError":              providerkit.FailureService,
	"ServiceUnavailable":         providerkit.FailureService,
})
