package core

import (
	"context"
	"slices"
)

// MediaGenerationModel is the product boundary, not an upstream MIME detector.
// State mutations and resource creation are inputs to media workflows, never
// independently selectable generation models. Ancillary result artifacts do not
// change the purpose of a media operation.
func MediaGenerationModel(m MediaModel) bool {
	if !slices.Contains([]string{"image", "video", "audio", "speech", "music"}, m.MediaType) || m.ResourceOperation != nil {
		return false
	}
	return m.SessionOperation == nil || m.MediaType == "video" && m.SessionOperation.Action == "preview" && !m.SessionOperation.Mutates
}

func MediaGenerationModels(models []MediaModel) []MediaModel {
	out := make([]MediaModel, 0, len(models))
	for _, m := range models {
		if MediaGenerationModel(m) {
			out = append(out, m)
		}
	}
	return out
}

// MediaPreparationCatalog is separate from discovery and the public executable
// catalog. Its canonical descriptors are usable only through a host-authorized
// media workflow, never through a manual model override.
type MediaPreparationCatalog interface {
	PreparationModels(context.Context) ([]MediaModel, error)
}

func PreparationSupports(workflow, preparation MediaModel) bool {
	if !MediaGenerationModel(workflow) || MediaGenerationModel(preparation) {
		return false
	}
	for _, input := range workflow.ResourceInputSchemas {
		if input.MaxCount <= 0 {
			continue
		}
		if operation := preparation.ResourceOperation; operation != nil && input.Kind == operation.Kind {
			if len(input.Subtypes) == 0 || len(operation.Subtypes) == 0 {
				return true
			}
			for _, subtype := range operation.Subtypes {
				if slices.Contains(input.Subtypes, subtype) {
					return true
				}
			}
		}
		if operation := preparation.SessionOperation; operation != nil && operation.Mutates && input.Kind == operation.Kind {
			return true
		}
		for _, output := range preparation.ResourceOutputs {
			if input.Kind != output.Kind {
				continue
			}
			if preparation.ResourceOperation == nil || len(input.Subtypes) == 0 || len(preparation.ResourceOperation.Subtypes) == 0 {
				return true
			}
			for _, subtype := range preparation.ResourceOperation.Subtypes {
				if slices.Contains(input.Subtypes, subtype) {
					return true
				}
			}
		}
	}
	return false
}

type preparationWorkflowKey struct{}
type preparationWorkflow struct{ provider, model, operation, subtype, publicModel string }

// WithMediaPreparationWorkflow is supplied by authenticated, scoped preparation
// handlers after resolving the consumer from the current public catalog. HTTP
// generation bodies cannot set this context. Core admission rechecks the pairing.
func WithMediaPreparationWorkflow(ctx context.Context, provider, model, operation, subtype string, publicModel ...string) context.Context {
	alias := ""
	if len(publicModel) > 0 {
		alias = publicModel[0]
	}
	return context.WithValue(ctx, preparationWorkflowKey{}, preparationWorkflow{provider, model, operation, subtype, alias})
}

func (s *TaskService) preparationModel(ctx context.Context, provider, model string) (MediaModel, bool) {
	workflow, ok := ctx.Value(preparationWorkflowKey{}).(preparationWorkflow)
	if !ok || workflow.provider != provider || workflow.model == model || workflow.operation != model {
		return MediaModel{}, false
	}
	consumer, ok := s.findModel(context.WithValue(ctx, preparationWorkflowKey{}, nil), provider, workflow.model)
	if !ok || !MediaGenerationModel(consumer) {
		return MediaModel{}, false
	}
	adapter, ok := s.providerFor(ctx, provider)
	if !ok {
		return MediaModel{}, false
	}
	catalog, ok := adapter.(MediaPreparationCatalog)
	if !ok {
		return MediaModel{}, false
	}
	models, err := catalog.PreparationModels(ctx)
	if err != nil {
		return MediaModel{}, false
	}
	for _, preparation := range models {
		if preparation.ID == model && !preparation.Manual && PreparationSupportsSubtype(consumer, preparation, workflow.subtype) {
			return preparation, true
		}
	}
	return MediaModel{}, false
}

func PreparationSupportsSubtype(workflow, preparation MediaModel, subtype string) bool {
	if !PreparationSupports(workflow, preparation) {
		return false
	}
	if preparation.ResourceOperation == nil {
		return subtype == ""
	}
	if subtype == "" || !slices.Contains(preparation.ResourceOperation.Subtypes, subtype) {
		return false
	}
	for _, input := range workflow.ResourceInputSchemas {
		if input.Kind == preparation.ResourceOperation.Kind && input.MaxCount > 0 && (len(input.Subtypes) == 0 || slices.Contains(input.Subtypes, subtype)) {
			return true
		}
	}
	return false
}

// PreparationModelsForWorkflow does not grant HTTP model visibility. The API
// must resolve the media consumer through its caller-authorized catalog first.
func (s *TaskService) PreparationModelsForWorkflow(ctx context.Context, provider, workflow string) ([]MediaModel, error) {
	consumer, ok := s.findModel(ctx, provider, workflow)
	if !ok || !MediaGenerationModel(consumer) {
		return nil, ErrModelUnavailable
	}
	adapter, ok := s.providerFor(ctx, provider)
	if !ok {
		return nil, ErrProviderUnavailable
	}
	catalog, ok := adapter.(MediaPreparationCatalog)
	if !ok {
		return nil, nil
	}
	models, err := catalog.PreparationModels(ctx)
	if err != nil {
		return nil, err
	}
	out := []MediaModel{}
	for _, m := range models {
		if !m.Manual && PreparationSupports(consumer, m) {
			out = append(out, m)
		}
	}
	return out, nil
}

// Unreviewed discovery is not executable product capability. Keep upstream
// account responses private; only reviewed media identities enter discovery.
func MediaDiscoveryIDs(ids []string, models []MediaModel) []string {
	allowed := map[string]bool{}
	for _, m := range models {
		if MediaGenerationModel(m) {
			allowed[m.ID] = true
		}
	}
	out := []string{}
	for _, id := range ids {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out
}
func MediaGenerationCatalog(catalog []ProviderDescriptor) []ProviderDescriptor {
	if catalog == nil {
		return nil
	}
	out := []ProviderDescriptor{}
	for _, p := range catalog {
		p.Models = MediaGenerationModels(p.Models)
		if len(p.Models) == 0 {
			continue
		}
		if !slices.ContainsFunc(p.Models, func(m MediaModel) bool { return m.ID == p.DefaultModel }) {
			p.DefaultModel = p.Models[0].ID
		}
		out = append(out, p)
	}
	return out
}
