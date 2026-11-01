package core

import "context"

// AdmissionConnectionResolver is implemented by the host connection manager.
// It pins the actual authorized revision independently of cost availability.
type AdmissionConnectionResolver interface {
	ResolveAdmissionConnection(context.Context, string) (ProviderConnectionScope, error)
}
type AdmissionScopeResolver func(context.Context, string) (ProviderConnectionScope, error)

func (s *TaskService) SetAdmissionScopeResolver(resolve AdmissionScopeResolver) {
	s.admissionScope = resolve
}
func (s *TaskService) admissionConnection(ctx context.Context, provider string) (ProviderConnectionScope, bool, error) {
	if manager, ok := s.directory.(AdmissionConnectionResolver); ok {
		scope, err := manager.ResolveAdmissionConnection(ctx, provider)
		return scope, true, err
	}
	if s.admissionScope != nil {
		scope, err := s.admissionScope(ctx, provider)
		return scope, true, err
	}
	return ProviderConnectionScope{}, false, nil
}
