package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

const taskColumns = `COALESCE((SELECT o.id::text FROM organizations o WHERE o.id=tasks.owner_id),''), COALESCE(actor_id::text,''), id, status, provider, model, media_type, prompt, request_options, webhook_url, provider_task_id, result_url, provider_result_url, last_frame_url, storage_key, result_content_type, result_size_bytes, result_sha256, stored_at, error, provider_usage, created_at, updated_at, owner_id, api_key_id, last_frame_storage_key, cost, provider_identity,provider_owner_id,storage_owner_id,result_deleted_at,(result_deleted_at IS NULL AND (storage_key IS NOT NULL OR EXISTS(SELECT 1 FROM stored_objects o WHERE o.task_id=tasks.id AND o.owner_id=tasks.owner_id AND o.deleted_at IS NULL))) AS has_stored_result,worker_started_at,worker_poll_errors,public_model,minimize_request_logging,had_image_input,identity_snapshot,COALESCE(project_id::text,''),project_version,template_snapshot,result_artifacts,COALESCE(to_jsonb(tasks)->'result_resource_bindings','[]'::jsonb)`

type TaskRepository struct {
	db *sql.DB
}

func NewTaskRepository(db *sql.DB) *TaskRepository {
	return &TaskRepository{db: db}
}

// CreateOrGet atomically persists the task, its Redis outbox marker and the
// initial webhook event. A repeated idempotency key returns the original task.
func (r *TaskRepository) CreateOrGet(ctx context.Context, task core.Task, idempotencyKey, requestHash string) (core.Task, bool, error) {
	if task.ProjectID != "" {
		id, err := uuid.Parse(task.ProjectID)
		if err != nil {
			return core.Task{}, false, core.ErrProjectInvalid
		}
		task.ProjectID = id.String()
	}
	optionsJSON, err := json.Marshal(task.Options)
	if err != nil {
		return core.Task{}, false, fmt.Errorf("encode task options: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Task{}, false, err
	}
	defer tx.Rollback()
	policy, err := lockAdmissionPolicy(ctx, tx)
	if err != nil {
		return core.Task{}, false, err
	}
	if task.OrganizationID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT minimize_request_logging FROM organizations WHERE id=$1 FOR SHARE`, task.OrganizationID).Scan(&task.MinimizeRequestLogging); err != nil {
			return core.Task{}, false, fmt.Errorf("read organization logging policy: %w", err)
		}
	}
	task.HadImageInput = len(task.Options.ImageURLs) > 0

	encodedCost, err := costJSON(task.Cost)
	if err != nil {
		return core.Task{}, false, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (
			id, status, provider, model, media_type, prompt, request_options, webhook_url,
			idempotency_key, request_hash, created_at, updated_at, owner_id, api_key_id, cost, provider_identity,provider_owner_id,storage_owner_id,actor_id,minimize_request_logging,had_image_input,project_id,template_snapshot
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (owner_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		task.ID, task.Status, task.Provider, task.Model, task.MediaType, task.Prompt, optionsJSON, task.WebhookURL,
		nullableString(idempotencyKey), requestHash, task.CreatedAt, task.UpdatedAt, identity.OwnerID(ctx), task.APIKeyID, encodedCost, task.ProviderIdentity, resourceOwner(task.ProviderOwnerID, identity.OwnerID(ctx)), resourceOwner(task.StorageOwnerID, identity.OwnerID(ctx)), nullableString(identity.ActorID(ctx)), task.MinimizeRequestLogging, task.HadImageInput, nullableString(task.ProjectID), nullableJSON(task.TemplateSnapshot),
	)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" && pg.ConstraintName == "tasks_project_id_fkey" {
			return core.Task{}, false, core.ErrProjectInvalid
		}
		return core.Task{}, false, fmt.Errorf("insert task: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.Task{}, false, err
	}
	if rows == 0 {
		var stored core.Task
		var storedHash string
		query := `SELECT ` + taskColumns + `, request_hash FROM tasks WHERE owner_id = $1 AND idempotency_key = $2`
		if err := scanTaskWithHash(tx.QueryRowContext(ctx, query, identity.OwnerID(ctx), idempotencyKey), &stored, &storedHash); err != nil {
			return core.Task{}, false, err
		}
		if storedHash != requestHash || (identity.ContentAuthorFilter(ctx) != "" && stored.ActorID != identity.ActorID(ctx)) {
			return core.Task{}, false, core.ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return core.Task{}, false, err
		}
		return stored, false, nil
	}

	if sound := task.Options.OverrideSound; sound != nil {
		if err := sourceAssetLock(ctx, tx, sound.OwnerID, sound.ReferenceID); err != nil {
			return core.Task{}, false, err
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reference_images WHERE owner_id::text=$1 AND id::text=$2 AND NOT deleting AND storage_key=$3 AND content_sha256=$4 AND size_bytes=$5)`, sound.OwnerID, sound.ReferenceID, sound.StorageKey, sound.SHA256, sound.SizeBytes).Scan(&valid); err != nil {
			return core.Task{}, false, err
		}
		if !valid {
			return core.Task{}, false, core.ErrOptionsInvalid
		}
	}
	if task.ProjectID != "" {
		if err := projectAccess(ctx, tx, task.ProjectID); err != nil {
			return core.Task{}, false, err
		}
	}
	if task.AdmissionError != nil {
		return core.Task{}, false, task.AdmissionError
	}

	if err := validateTaskResourceSnapshots(ctx, tx, task); err != nil {
		return core.Task{}, false, err
	}
	if task.APIKeyID != nil {
		if err := checkAdmissionPolicy(ctx, tx, policy); err != nil {
			return core.Task{}, false, err
		}
	}
	if err := checkProviderAssets(ctx, tx, task); err != nil {
		return core.Task{}, false, err
	}
	var captured []byte
	if err := tx.QueryRowContext(ctx, `SELECT identity_snapshot FROM tasks WHERE id=$1`, task.ID).Scan(&captured); err != nil {
		return core.Task{}, false, err
	}
	task.IdentitySnapshot = captured
	if err := checkTaskModelAccess(ctx, tx, &task); err != nil {
		return core.Task{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET public_model=$2 WHERE id=$1`, task.ID, task.PublicModel); err != nil {
		return core.Task{}, false, err
	}

	// Apply the latest rate only after idempotency resolution, under the same
	// transaction as acceptance. A retry keeps its original pricing snapshot.
	if err := applyTaskMarkup(ctx, tx, &task, true); err != nil {
		return core.Task{}, false, err
	}
	if task.Cost != nil {
		encodedCost, err = costJSON(task.Cost)
		if err != nil {
			return core.Task{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET cost=$2 WHERE id=$1`, task.ID, encodedCost); err != nil {
			return core.Task{}, false, err
		}
	}
	if task.RequireCost && task.Cost == nil {
		return core.Task{}, false, core.ErrPricingUnavailable
	}
	if task.Cost != nil && task.MaxExpectedUSD != nil {
		expected, err := task.Cost.CustomerExpected()
		if err != nil {
			return core.Task{}, false, err
		}
		if expected > *task.MaxExpectedUSD {
			return core.Task{}, false, core.ErrEstimateChanged
		}
	}
	if task.APIKeyID != nil {
		if err := checkAvailableBudget(ctx, tx, task); err != nil {
			return core.Task{}, false, err
		}
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO task_queue_outbox (task_id) VALUES ($1)`, task.ID); err != nil {
		return core.Task{}, false, fmt.Errorf("insert task queue outbox: %w", err)
	}
	if err := insertWebhookDelivery(ctx, tx, core.EventTaskCreated, task); err != nil {
		return core.Task{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return core.Task{}, false, err
	}
	return task, true, nil
}

func (r *TaskRepository) Get(ctx context.Context, id string) (core.Task, error) {
	var task core.Task
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE id = $1`
	args := []any{id}
	if _, ok := identity.UserFromContext(ctx); ok {
		query += ` AND owner_id = $2 AND ($3='' OR actor_id::text=$3)`
		args = append(args, identity.OwnerID(ctx), identity.ContentAuthorFilter(ctx))

	}
	if err := scanTask(r.db.QueryRowContext(ctx, query, args...), &task); err != nil {
		return core.Task{}, err
	}
	return task, nil
}

func (r *TaskRepository) ListRecent(ctx context.Context, limit int) ([]core.Task, error) {
	return r.ListRecentPage(ctx, limit, 0)
}
func (r *TaskRepository) ListRecentPage(ctx context.Context, limit, offset int) ([]core.Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks`
	args := []any{}
	if _, ok := identity.UserFromContext(ctx); ok {
		query += ` WHERE owner_id = $1 AND ($3='' OR actor_id::text=$3) ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $4`
		args = append(args, identity.OwnerID(ctx), limit, identity.ContentAuthorFilter(ctx), offset)
	} else {
		query += ` ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list recent tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]core.Task, 0, limit)
	for rows.Next() {
		var task core.Task
		if err := scanTask(rows, &task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list recent tasks: %w", err)
	}
	return tasks, nil
}

func (r *TaskRepository) MarkEnqueued(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM task_queue_outbox WHERE task_id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark task enqueued: %w", err)
	}
	return nil
}

func (r *TaskRepository) MarkRunning(ctx context.Context, id string) error {
	return r.transition(ctx, id, core.EventTaskStarted, `
		UPDATE tasks SET status = 'running', updated_at = NOW(), worker_started_at=NOW(),
 cost = CASE WHEN cost IS NULL THEN NULL ELSE jsonb_set(cost, '{submitted_at}', to_jsonb(NOW())) END
		WHERE id = $1 AND status = 'queued'
		RETURNING `+taskColumns, nil)
}

func (r *TaskRepository) SetProviderTaskID(ctx context.Context, id, providerTaskID string) error {
	result, err := r.workerExec(ctx, id, `
		UPDATE tasks SET provider_task_id = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'running'`, id, providerTaskID)
	return changed(result, err, "store provider task id")
}

func (r *TaskRepository) SetProviderUsage(ctx context.Context, id string, usage core.ProviderUsage) error {
	return r.updateCost(ctx, id, func(task *core.Task) {
		task.Usage = &usage
		if task.Cost != nil {
			if task.Cost.MeterUsage(usage) {
				now := time.Now().UTC()
				task.Cost.UsageRecordedAt = &now
			}
		}
	})
}

func (r *TaskRepository) SetProviderResultURL(ctx context.Context, id, resultURL string) error {
	result, err := r.workerExec(ctx, id, `
		UPDATE tasks SET provider_result_url = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'running'`, id, nullableString(resultURL))
	return changed(result, err, "store provider result URL")
}

func (r *TaskRepository) MarkSucceeded(ctx context.Context, id string, media core.StoredMedia, lastFrameURL string) error {
	if err := validateSessionPreviewArtifacts(media.Resources, media.Artifacts); err != nil {
		return err
	}
	artifacts := media.Artifacts
	if artifacts == nil {
		artifacts = []core.StoredArtifact{}
	}
	encoded, err := json.Marshal(artifacts)
	if err != nil {
		return err
	}
	var publicURL any
	if media.StorageKey != "" {
		publicURL = "/v1/generations/" + id + "/content"
	}
	args := []any{publicURL, nullableString(media.SourceURL), nullableString(lastFrameURL), nullableString(media.StorageKey), nullableString(media.ContentType), media.SizeBytes, nullableString(media.SHA256), nullableString(media.LastFrameStorageKey), encoded}
	resourceAssignment := ""
	hooks := []func(*sql.Tx) error{func(tx *sql.Tx) error {
		return validateSessionActionCompletion(ctx, tx, id, media.Resources)
	}}
	if len(media.Resources) > 0 {
		resourceAssignment = ", result_resource_bindings = $11"
		args = append(args, []byte("[]"))
		hooks = append(hooks, func(tx *sql.Tx) error {
			refs, e := captureTaskResourceBindings(ctx, tx, id, media.Resources)
			if refs == nil {
				refs = []core.ResourceReference{}
			}
			if e != nil {
				return e
			}
			encoded, e := json.Marshal(refs)
			if e != nil {
				return e
			}
			args[9] = encoded
			return nil
		})
	}
	return r.transition(ctx, id, core.EventTaskSucceeded, `UPDATE tasks SET status='succeeded',result_url=$2,provider_result_url=CASE WHEN minimize_request_logging THEN NULL ELSE $3 END,last_frame_url=$4,storage_key=$5,result_content_type=$6,result_size_bytes=$7,result_sha256=$8,stored_at=NOW(),error=NULL,updated_at=NOW(),last_frame_storage_key=$9,result_artifacts=$10`+resourceAssignment+` WHERE id=$1 AND status='running' RETURNING `+taskColumns, args, hooks...)
}

func (r *TaskRepository) MarkFailed(ctx context.Context, id, message string) error {
	return r.transition(ctx, id, core.EventTaskFailed, `
		UPDATE tasks
		SET status = 'failed', error = CASE WHEN minimize_request_logging THEN 'Generation failed' ELSE $2 END, updated_at = NOW()
		WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING `+taskColumns, []any{message}, func(tx *sql.Tx) error {
		return failTaskSessionAction(ctx, tx, id)
	})
}

func (r *TaskRepository) transition(ctx context.Context, id string, eventType core.TaskEventType, query string, args []any, hooks ...func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockProfitAssignment(ctx, tx); err != nil {
		return err
	}
	var key *string
	if err = tx.QueryRowContext(ctx, `SELECT api_key_id FROM tasks WHERE id=$1`, id).Scan(&key); err != nil {
		return err
	}
	if key != nil {
		var ignored string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM user_api_keys WHERE id=$1 FOR NO KEY UPDATE`, *key).Scan(&ignored); err != nil {
			return err
		}
	}
	if err = checkTaskClaim(ctx, tx, id); err != nil {
		return err
	}
	// Keep pending paid receipts safe from legacy terminal writes after a
	// non-destructive rollback. Only the already verified live worker claim
	// authorizes cleanup, locally to this exact task and transaction.
	if core.ClaimToken(ctx) != "" {
		if _, err = tx.ExecContext(ctx, `SELECT set_config('gateway.accepted_receipt_terminal',$1,true)`, id); err != nil {
			return err
		}
	}
	for _, hook := range hooks {
		if err := hook(tx); err != nil {
			return err
		}
	}
	params := append([]any{id}, args...)
	var task core.Task
	if err := scanTask(tx.QueryRowContext(ctx, query, params...), &task); err != nil {
		if errors.Is(err, core.ErrTaskNotFound) {
			return fmt.Errorf("transition task: task state changed concurrently")
		}
		return err
	}
	if err := insertWebhookDelivery(ctx, tx, eventType, task); err != nil {
		return err
	}
	if task.MinimizeRequestLogging && (task.Status == core.TaskSucceeded || task.Status == core.TaskFailed) {
		// Preserve only the options required to render task dimensions and
		// reconcile provider usage. This runs in the terminal transition so no
		// completed task can be committed with its original request content.
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET prompt='',template_snapshot=CASE WHEN template_snapshot IS NULL THEN NULL ELSE jsonb_build_object('id',template_snapshot->'id','revision',template_snapshot->'revision') END,
			request_options=jsonb_strip_nulls(jsonb_build_object(
				'resolution',request_options->'resolution',
				'ratio',request_options->'ratio',
				'duration',request_options->'duration',
				'generate_audio',request_options->'generate_audio')),
			provider_usage=CASE WHEN provider_usage IS NULL THEN NULL ELSE provider_usage - 'raw' END,
			webhook_url=NULL,provider_result_url=NULL,
			last_frame_url=CASE WHEN status='succeeded' THEN last_frame_url ELSE NULL END
			WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *TaskRepository) ListRecoverable(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM tasks
		WHERE status IN ('queued', 'running')
		ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list recoverable tasks: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan recoverable task: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *TaskRepository) ListDueWebhookDeliveries(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM webhook_deliveries
		WHERE status = 'pending' AND next_attempt_at <= NOW()
		ORDER BY next_attempt_at, created_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list due webhook deliveries: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *TaskRepository) GetWebhookDelivery(ctx context.Context, id string) (core.WebhookDelivery, error) {
	var delivery core.WebhookDelivery
	err := r.db.QueryRowContext(ctx, `
		SELECT id, task_id, event_type, webhook_url, payload, status, attempts, next_attempt_at, created_at
		FROM webhook_deliveries WHERE id = $1`, id).Scan(
		&delivery.ID, &delivery.TaskID, &delivery.EventType, &delivery.URL, &delivery.Payload,
		&delivery.Status, &delivery.Attempts, &delivery.NextAttemptAt, &delivery.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return core.WebhookDelivery{}, core.ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return core.WebhookDelivery{}, fmt.Errorf("get webhook delivery: %w", err)
	}
	return delivery, nil
}

func (r *TaskRepository) MarkWebhookDelivered(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE webhook_deliveries
		SET status = 'delivered', attempts = attempts + 1, delivered_at = NOW(), updated_at = NOW(), last_error = NULL,
			webhook_url=CASE WHEN EXISTS(SELECT 1 FROM tasks WHERE id=task_id AND minimize_request_logging) THEN '' ELSE webhook_url END
		WHERE id = $1 AND status = 'pending'`, id)
	return changed(result, err, "mark webhook delivered")
}

func (r *TaskRepository) MarkWebhookRetry(ctx context.Context, id string, attempts int, nextAttempt time.Time, message string, terminal bool) error {
	status := core.WebhookPending
	if terminal {
		status = core.WebhookFailed
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE webhook_deliveries
		SET status = $2, attempts = $3, next_attempt_at = $4,
			last_error = CASE WHEN EXISTS(SELECT 1 FROM tasks WHERE id=task_id AND minimize_request_logging) THEN NULL ELSE $5 END,
			webhook_url=CASE WHEN $2='failed' AND EXISTS(SELECT 1 FROM tasks WHERE id=task_id AND minimize_request_logging) THEN '' ELSE webhook_url END,
			updated_at = NOW()
		WHERE id = $1 AND status = 'pending'`, id, status, attempts, nextAttempt, message)
	return changed(result, err, "record webhook attempt")
}

type rowScanner interface {
	Scan(...any) error
}

func scanTask(row rowScanner, task *core.Task) error {
	var options []byte
	var usage, cost, snapshot, templateSnapshot, artifacts, resourceRefs []byte
	err := row.Scan(
		&task.OrganizationID, &task.ActorID, &task.ID, &task.Status, &task.Provider, &task.Model, &task.MediaType, &task.Prompt, &options, &task.WebhookURL,
		&task.ProviderTaskID, &task.ResultURL, &task.ProviderResultURL, &task.LastFrameURL, &task.StorageKey,
		&task.ResultType, &task.ResultSize, &task.ResultSHA256, &task.StoredAt,
		&task.Error, &usage, &task.CreatedAt, &task.UpdatedAt,
		&task.OwnerID, &task.APIKeyID, &task.LastFrameStorageKey, &cost, &task.ProviderIdentity, &task.ProviderOwnerID, &task.StorageOwnerID, &task.ResultDeletedAt, &task.HasStoredResult, &task.WorkerStartedAt, &task.WorkerPollErrors, &task.PublicModel, &task.MinimizeRequestLogging, &task.HadImageInput, &snapshot, &task.ProjectID, &task.ProjectVersion, &templateSnapshot, &artifacts, &resourceRefs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("scan task: %w", err)
	}
	task.IdentitySnapshot = snapshot
	task.TemplateSnapshot = templateSnapshot
	if len(resourceRefs) > 0 {
		if err := json.Unmarshal(resourceRefs, &task.StoredResourceReferences); err != nil {
			return fmt.Errorf("decode stored resource bindings: %w", err)
		}
	}
	if len(artifacts) > 0 {
		if err := json.Unmarshal(artifacts, &task.StoredArtifacts); err != nil {
			return fmt.Errorf("decode stored artifacts: %w", err)
		}
	}
	if err := json.Unmarshal(options, &task.Options); err != nil {
		return fmt.Errorf("decode task options: %w", err)
	}
	if len(cost) > 0 && string(cost) != "null" {
		if err := json.Unmarshal(cost, &task.Cost); err != nil {
			return err
		}
	}
	if len(usage) > 0 {
		task.Usage = &core.ProviderUsage{}
		if err := json.Unmarshal(usage, task.Usage); err != nil {
			return fmt.Errorf("decode provider usage: %w", err)
		}
	}
	return nil
}

func scanTaskWithHash(row rowScanner, task *core.Task, requestHash *string) error {
	var options []byte
	var usage, cost, snapshot, templateSnapshot, artifacts, resourceRefs []byte
	err := row.Scan(
		&task.OrganizationID, &task.ActorID, &task.ID, &task.Status, &task.Provider, &task.Model, &task.MediaType, &task.Prompt, &options, &task.WebhookURL,
		&task.ProviderTaskID, &task.ResultURL, &task.ProviderResultURL, &task.LastFrameURL, &task.StorageKey,
		&task.ResultType, &task.ResultSize, &task.ResultSHA256, &task.StoredAt,
		&task.Error, &usage, &task.CreatedAt, &task.UpdatedAt,
		&task.OwnerID, &task.APIKeyID, &task.LastFrameStorageKey, &cost, &task.ProviderIdentity, &task.ProviderOwnerID, &task.StorageOwnerID, &task.ResultDeletedAt, &task.HasStoredResult, &task.WorkerStartedAt, &task.WorkerPollErrors, &task.PublicModel, &task.MinimizeRequestLogging, &task.HadImageInput, &snapshot, &task.ProjectID, &task.ProjectVersion, &templateSnapshot, &artifacts, &resourceRefs,
		requestHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("scan idempotent task: %w", err)
	}
	task.IdentitySnapshot = snapshot
	task.TemplateSnapshot = templateSnapshot
	if len(resourceRefs) > 0 {
		if err := json.Unmarshal(resourceRefs, &task.StoredResourceReferences); err != nil {
			return fmt.Errorf("decode stored resource bindings: %w", err)
		}
	}
	if len(artifacts) > 0 {
		if err := json.Unmarshal(artifacts, &task.StoredArtifacts); err != nil {
			return fmt.Errorf("decode stored artifacts: %w", err)
		}
	}
	if err := json.Unmarshal(options, &task.Options); err != nil {
		return fmt.Errorf("decode task options: %w", err)
	}
	if len(cost) > 0 && string(cost) != "null" {
		if err := json.Unmarshal(cost, &task.Cost); err != nil {
			return err
		}
	}
	if len(usage) > 0 {
		task.Usage = &core.ProviderUsage{}
		if err := json.Unmarshal(usage, task.Usage); err != nil {
			return fmt.Errorf("decode provider usage: %w", err)
		}
	}
	return nil
}

func insertWebhookDelivery(ctx context.Context, tx *sql.Tx, eventType core.TaskEventType, task core.Task) error {
	if task.OrganizationID != "" {
		task.Cost = task.Cost.ConsumptionView()
	}
	if task.WebhookURL == nil {
		return nil
	}
	id := uuid.NewString()
	createdAt := time.Now().UTC()
	view := core.GenerationView(task)
	payload, err := json.Marshal(struct {
		ID        string          `json:"id"`
		Object    string          `json:"object"`
		Type      string          `json:"type"`
		CreatedAt int64           `json:"created_at"`
		Data      core.Generation `json:"data"`
	}{ID: id, Object: "event", Type: core.GenerationEventType(eventType), CreatedAt: createdAt.Unix(), Data: view})
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO webhook_deliveries (
			id, task_id, event_type, webhook_url, payload, status, attempts,
			next_attempt_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'pending', 0, $6, $6, $6)`,
		id, task.ID, eventType, *task.WebhookURL, payload, createdAt,
	)
	if err != nil {
		return fmt.Errorf("insert webhook delivery: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func changed(result sql.Result, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if rows == 0 {
		return fmt.Errorf("%s: state changed concurrently", operation)
	}
	return nil
}

func resourceOwner(value, owner string) string {
	if value == "" {
		return owner
	}
	return value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}
