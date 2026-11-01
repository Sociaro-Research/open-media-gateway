package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func (p *managedProvider) resourceAdapter(ctx context.Context, current bool) (context.Context, core.ResourceProvider, error) {
	expected := core.ProviderRevision(ctx)
	adapter, err := p.manager.resolve(ctx, p.id, current && expected == "", expected)
	if err != nil {
		return ctx, nil, err
	}
	resource, ok := adapter.(core.ResourceProvider)
	if !ok {
		return ctx, nil, core.ErrProviderUnavailable
	}
	return p.connectionScope(ctx, adapter, expected), resource, nil
}
func (p *managedProvider) CreateResource(ctx context.Context, request core.ResourceCreateRequest) (core.ResourceJob, error) {
	ctx, adapter, err := p.resourceAdapter(ctx, true)
	if err != nil {
		return core.ResourceJob{}, err
	}
	return adapter.CreateResource(ctx, request)
}
func (p *managedProvider) GetResourceJob(ctx context.Context, kind, handle string) (core.ResourceJob, error) {
	ctx, adapter, err := p.resourceAdapter(ctx, false)
	if err != nil {
		return core.ResourceJob{}, err
	}
	return adapter.GetResourceJob(ctx, kind, handle)
}
func (p *managedProvider) ListResources(ctx context.Context, kind string, preset bool, page, size int) (core.ResourcePage, error) {
	ctx, adapter, err := p.resourceAdapter(ctx, true)
	if err != nil {
		return core.ResourcePage{}, err
	}
	return adapter.ListResources(ctx, kind, preset, page, size)
}
func (p *managedProvider) DeleteResource(ctx context.Context, resource core.ResourceSnapshot) (core.ResourceJob, error) {
	ctx, adapter, err := p.resourceAdapter(ctx, false)
	if err != nil {
		return core.ResourceJob{}, err
	}
	return adapter.DeleteResource(ctx, resource)
}
func (a *connectionAdapter) resourceProvider() (core.ResourceProvider, error) {
	provider, ok := a.MediaProvider.(core.ResourceProvider)
	if !ok {
		return nil, core.ErrProviderUnavailable
	}
	return provider, nil
}
func (a *connectionAdapter) CreateResource(ctx context.Context, request core.ResourceCreateRequest) (core.ResourceJob, error) {
	provider, err := a.resourceProvider()
	if err != nil {
		return core.ResourceJob{}, err
	}
	job, err := provider.CreateResource(ctx, request)
	job.Error = a.redactText(job.Error)
	job.Warning = a.redactText(job.Warning)
	return job, a.redact(err)
}
func (a *connectionAdapter) GetResourceJob(ctx context.Context, kind, handle string) (core.ResourceJob, error) {
	provider, err := a.resourceProvider()
	if err != nil {
		return core.ResourceJob{}, err
	}
	job, err := provider.GetResourceJob(ctx, kind, handle)
	job.Error = a.redactText(job.Error)
	job.Warning = a.redactText(job.Warning)
	return job, a.redact(err)
}
func (a *connectionAdapter) ListResources(ctx context.Context, kind string, preset bool, page, size int) (core.ResourcePage, error) {
	provider, err := a.resourceProvider()
	if err != nil {
		return core.ResourcePage{}, err
	}
	result, err := provider.ListResources(ctx, kind, preset, page, size)
	return result, a.redact(err)
}
func (a *connectionAdapter) DeleteResource(ctx context.Context, resource core.ResourceSnapshot) (core.ResourceJob, error) {
	provider, err := a.resourceProvider()
	if err != nil {
		return core.ResourceJob{}, err
	}
	job, err := provider.DeleteResource(ctx, resource)
	job.Error = a.redactText(job.Error)
	job.Warning = a.redactText(job.Warning)
	return job, a.redact(err)
}

func (p *managedProvider) ValidateResourceDeletion(ctx context.Context, resource core.ResourceSnapshot) error {
	ctx, adapter, err := p.resourceAdapter(ctx, false)
	if err != nil {
		return err
	}
	if validator, ok := adapter.(core.ResourceDeletionValidator); ok {
		return validator.ValidateResourceDeletion(ctx, resource)
	}
	return nil
}
func (a *connectionAdapter) ValidateResourceDeletion(ctx context.Context, resource core.ResourceSnapshot) error {
	if validator, ok := a.MediaProvider.(core.ResourceDeletionValidator); ok {
		return a.redact(validator.ValidateResourceDeletion(ctx, resource))
	}
	return nil
}
