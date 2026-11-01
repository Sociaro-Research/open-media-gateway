package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
)

// The source row lock serializes new binding admission with source-result
// deletion. Completion rechecks immutable identity, while accepted binding work
// retains its proven source if the owner subsequently deletes the local archive.
func validateTaskGeneratedMedia(ctx context.Context, tx *sql.Tx, task core.Task, fresh bool) error {
	invalid := fmt.Errorf("%w: generated source is unavailable or changed", core.ErrOptionsInvalid)
	if task.Options.ResourceCreation == nil || task.Options.ResourceCreation.GeneratedMedia == nil {
		return nil
	}
	source := task.Options.ResourceCreation.GeneratedMedia
	if source.TaskID == task.ID || !validResourceUUID(source.TaskID) || source.Inspection == nil || source.Inspection.Type != source.MediaType || source.Inspection.ReferenceID != source.TaskID || source.Inspection.SHA256 != source.SHA256 || source.Duration == nil || source.Inspection.Duration != *source.Duration {
		return invalid
	}
	var owner, powner, provider, revision, model, kind, status string
	var created time.Time
	var deleted sql.NullTime
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT owner_id::text,provider_owner_id::text,provider,provider_identity,model,media_type,status,created_at,result_deleted_at,result_artifacts FROM tasks WHERE id=$1 FOR SHARE`, source.TaskID).Scan(&owner, &powner, &provider, &revision, &model, &kind, &status, &created, &deleted, &raw)
	if err == sql.ErrNoRows {
		return invalid
	}
	if err != nil {
		return err
	}
	if owner != task.OwnerID || powner != task.ProviderOwnerID || provider != task.Provider || revision != task.ProviderIdentity || model != source.Model || core.GeneratedTaskMediaType(kind) != source.MediaType || status != "succeeded" || !created.Equal(source.CreatedAt) || fresh && (deleted.Valid || core.ResourceSourcesExpired([]core.ResourceSourceEvidence{core.GeneratedResourceEvidence(source)}, time.Now())) {
		return invalid
	}
	var artifacts []core.StoredArtifact
	if err = json.Unmarshal(raw, &artifacts); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if artifact.ID != source.ArtifactID {
			continue
		}
		if artifact.MediaType == source.MediaType && artifact.ProviderID == source.OutputID && artifact.SHA256 == source.SHA256 && artifact.SizeBytes == source.Inspection.SizeBytes && mediafile.CanonicalContentType(artifact.ContentType) == source.Inspection.ContentType && artifact.StorageKey != "" {
			return nil
		}
		return invalid
	}
	return invalid
}
