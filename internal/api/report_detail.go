package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

// Resolve the report first: its financial scope may be narrower than task access.
func (s *Server) reportRecord(w http.ResponseWriter, r *http.Request) (*http.Request, core.CostReportRow, core.Task, bool) {
	var empty core.CostReportRow
	if strings.HasPrefix(r.URL.Path, "/v1/admin/reports/") {
		if credentialOrganization(r.Context()) != "" {
			writeError(w, 403, "forbidden", "use this organization's report endpoint")
			return r, empty, core.Task{}, false
		}
		r = r.WithContext(identity.WithPersonalData(r.Context()))
	}
	if _, ok := personalOwner(w, r); !ok {
		return r, empty, core.Task{}, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "invalid_request", "invalid generation ID")
		return r, empty, core.Task{}, false
	}
	r.SetPathValue("id", id.String())
	all := strings.HasPrefix(r.URL.Path, "/v1/system/reports/")
	if all {
		admin, err := s.systemAdmin(r.Context())
		if err != nil {
			systemError(w, err)
			return r, empty, core.Task{}, false
		}
		if !admin {
			writeError(w, 403, "forbidden", "administrator access is required")
			return r, empty, core.Task{}, false
		}
	}
	reader, ok := s.service.(interface {
		CostReport(context.Context, core.ReportFilter) ([]core.CostReportRow, error)
	})
	if !ok {
		writeError(w, 503, "unavailable", "report details are unavailable")
		return r, empty, core.Task{}, false
	}
	rows, err := reader.CostReport(r.Context(), core.ReportFilter{GenerationID: id.String(), AllOrganizations: all, From: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), Limit: 1})
	if err != nil {
		writeError(w, 503, "unavailable", "report could not be loaded")
		return r, empty, core.Task{}, false
	}
	if len(rows) == 0 {
		writeError(w, 404, "not_found", "generation report not found")
		return r, empty, core.Task{}, false
	}
	row := rows[0]
	if all {
		// System Reports contains organization requests, never private personal tasks.
		if row.OrganizationID == "" {
			writeError(w, 404, "not_found", "generation report not found")
			return r, empty, core.Task{}, false
		}
		r = r.WithContext(identity.WithOrganizationRole(r.Context(), row.OrganizationID, "system_admin"))
	}
	task, err := s.service.GetTask(r.Context(), id.String())
	if errors.Is(err, core.ErrTaskNotFound) || err == nil && (task.ID != row.ID || task.OwnerID != identity.OwnerID(r.Context()) || (identity.FinancialAuthorFilter(r.Context()) != "" && task.ActorID != identity.ActorID(r.Context()))) {
		writeError(w, 404, "not_found", "generation report not found")
		return r, empty, core.Task{}, false
	}
	if err != nil {
		writeError(w, 503, "unavailable", "generation could not be loaded")
		return r, empty, core.Task{}, false
	}
	return r, row, task, true
}

func (s *Server) reportDetail(w http.ResponseWriter, r *http.Request) {
	r, row, task, ok := s.reportRecord(w, r)
	if !ok {
		return
	}
	role := identity.OrganizationFromContext(r.Context()).Role
	content := role == "" || org.OwnContent(role)
	if !content || task.MinimizeRequestLogging {
		row.Usage = safeUsage(row.Usage)
	}
	data := map[string]any{"report": row, "generation": s.generationView(r, task), "content_access": content, "request_logging": "full"}
	if task.MinimizeRequestLogging {
		data["request_logging"] = "minimized"
	}
	if content {
		if !task.MinimizeRequestLogging {
			data["request"] = map[string]any{"prompt": task.Prompt, "options": task.Options, "webhook_url": task.WebhookURL}
		} else {
			// Match the persisted minimal-log allowlist, including for in-flight tasks.
			data["request"] = map[string]any{"options": map[string]any{"resolution": task.Options.Resolution, "ratio": task.Options.Ratio, "duration": task.Options.Duration, "generate_audio": task.Options.GenerateAudio}}
		}
		data["processing"] = map[string]any{"started_at": task.WorkerStartedAt, "poll_errors": task.WorkerPollErrors, "had_image_input": task.HadImageInput}
		links := map[string]string{}
		// Use report-scoped routes so browser sessions can open personal and system results.
		base := strings.TrimSuffix(r.URL.Path, "/")
		if task.ResultDeletedAt == nil && task.Status == core.TaskSucceeded && (task.HasStoredResult || task.StorageKey != nil) {
			links["content_url"] = base + "/content"
			links["download_url"] = base + "/content?download=1"
			if task.LastFrameStorageKey != nil {
				links["last_frame_url"] = base + "/last-frame"
			}
		}
		data["links"] = links
	}
	s.writeViewerJSON(w, r, 200, data)
}

func (s *Server) reportArtifact(w http.ResponseWriter, r *http.Request) {
	r, _, _, ok := s.reportRecord(w, r)
	if !ok {
		return
	}
	role := identity.OrganizationFromContext(r.Context()).Role
	if role != "" && !org.OwnContent(role) {
		writeError(w, 403, "forbidden", "generation content is restricted for this role")
		return
	}
	s.serveTaskMedia(w, r, r.PathValue("id"), strings.HasSuffix(r.URL.Path, "/last-frame"))
}
