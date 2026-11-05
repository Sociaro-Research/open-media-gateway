package bytedance

import "github.com/Sociaro-Research/open-media-gateway/internal/providerkit"

// Exact reviewed native codes only; unknown codes and provider prose remain private.
// https://ai.byteplus.com/ark/region:ap-southeast-1/docs/error-codes
var failurePolicy = providerkit.NewFailurePolicy("BytePlus", map[string]providerkit.FailureKind{
	"MissingParameter":                      providerkit.FailureInput,
	"InvalidParameter":                      providerkit.FailureInput,
	"InvalidParameter.TaskTypeConstraint":   providerkit.FailureInput,
	"InvalidParameter.TaskTypeMismatch":     providerkit.FailureInput,
	"AuthenticationError":                   providerkit.FailureCredentials,
	"AccessDenied":                          providerkit.FailurePermission,
	"OperationDenied.PermissionDenied":      providerkit.FailurePermission,
	"InvalidEndpoint.ClosedEndpoint":        providerkit.FailureConfiguration,
	"InvalidEndpointOrModel.NotFound":       providerkit.FailureConfiguration,
	"ModelNotOpen":                          providerkit.FailureConfiguration,
	"OperationDenied.ServiceNotOpen":        providerkit.FailureConfiguration,
	"RateLimitExceeded.EndpointRPMExceeded": providerkit.FailureRateLimit,
	"RateLimitExceeded.EndpointTPMExceeded": providerkit.FailureRateLimit,
	"AccountRateLimitExceeded":              providerkit.FailureRateLimit,
	"InflightBatchsizeExceeded":             providerkit.FailureRateLimit,
	"QuotaExceeded":                         providerkit.FailureQuota,
	"AccountOverdueError":                   providerkit.FailureBalance,
	"OperationDenied.ServiceOverdue":        providerkit.FailureBalance,
	"SensitiveContentDetected":              providerkit.FailureContentPolicy,
	"InputTextSensitiveContentDetected":     providerkit.FailureContentPolicy,
	"InputImageSensitiveContentDetected":    providerkit.FailureContentPolicy,
	"InputVideoSensitiveContentDetected":    providerkit.FailureContentPolicy,
	"InputAudioSensitiveContentDetected":    providerkit.FailureContentPolicy,
	"OutputImageSensitiveContentDetected":   providerkit.FailureContentPolicy,
	"OutputVideoSensitiveContentDetected":   providerkit.FailureContentPolicy,
	"OutputAudioSensitiveContentDetected":   providerkit.FailureContentPolicy,
	"InternalServiceError":                  providerkit.FailureService,
})
