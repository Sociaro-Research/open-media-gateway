package core

import "context"

// UnchargedImageRejection is a submission outcome for a provider that explicitly
// reported zero generated images and zero in each of its three billable token
// categories. It carries no provider text, identifiers or payload.
type UnchargedImageRejection struct{}

func (UnchargedImageRejection) Error() string {
	return "The provider rejected all requested images without charge."
}

func (UnchargedImageRejection) Usage() ProviderUsage {
	return ProviderUsage{Metrics: []UsageMetric{
		{Name: "generated_images", Unit: "image", Quantity: 0},
		{Name: "text_input_tokens", Unit: "token", Quantity: 0},
		{Name: "image_input_tokens", Unit: "token", Quantity: 0},
		{Name: "image_output_tokens", Unit: "token", Quantity: 0},
	}}
}

// UnchargedImageRepository commits the verified outcome under the live claim.
type UnchargedImageRepository interface {
	MarkImagesRejectedWithoutCharge(context.Context, Task, UnchargedImageRejection) error
}
