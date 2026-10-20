package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

func csvText(s string) string {
	if strings.ContainsAny(firstCharacter(strings.TrimLeft(s, " \t\r\n")), "=+-@") || strings.ContainsAny(firstCharacter(s), "\t\r\n") {
		return "'" + s
	}
	return s
}
func firstCharacter(s string) string {
	if s == "" {
		return ""
	}
	return s[:1]
}
func csvMoney(v *money.USD) string {
	if v == nil {
		return ""
	}
	return v.String()
}
func (s *Server) exportCostReport(w http.ResponseWriter, r *http.Request, f core.ReportFilter) {
	reader, ok := s.service.(interface {
		WalkCostReport(context.Context, core.ReportFilter, func(core.CostReportRow) error) error
	})
	if !ok {
		writeError(w, 503, "unavailable", "report export is unavailable")
		return
	}
	// Spool before sending headers: database failures must never produce a successful but partial CSV.
	file, err := os.CreateTemp("", "gateway-report-*.csv")
	if err != nil {
		writeError(w, 503, "unavailable", "report export is unavailable")
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	writer := csv.NewWriter(file)
	writer.UseCRLF = true
	admin, err := s.systemAdmin(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	sales, err := s.isClientManager(r.Context())
	if err != nil {
		systemError(w, err)
		return
	}
	commercial := admin || sales
	member := !admin
	headers := []string{"Date (UTC)", "Organization", "Organization ID", "User", "User ID", "API key", "API key ID", "Provider", "Model", "Status", "Tokens"}
	if member {
		headers = append(headers, "Cost (USD)")
	} else {
		headers = append(headers, "Estimated cost (USD)", "Cost (USD)", "Confirmed cost (USD)", "Provider charged (USD)", "Package units", "Customer charge (USD)", "Markup (%)")
	}
	if commercial {
		headers = append(headers, "Profit status", "Profit (USD)", "Profit recognized at (UTC)")
	}
	headers = append(headers, "Identity source")
	if err = writer.Write(headers); err != nil {
		writeError(w, 503, "unavailable", "report export failed")
		return
	}
	err = reader.WalkCostReport(r.Context(), f, func(row core.CostReportRow) error {
		key := ""
		if row.KeyID != nil {
			key = *row.KeyID
		}
		usage := ""
		if row.Usage != nil {
			metrics := []core.UsageMetric{}
			for _, m := range row.Usage.Metrics {
				if m.Unit == "token" {
					metrics = append(metrics, m)
				}
			}
			b, e := json.Marshal(metrics)
			if e != nil {
				return e
			}
			usage = string(b)
		}
		values := []string{row.CreatedAt.UTC().Format(time.RFC3339Nano), csvText(row.OrganizationName), row.OrganizationID, csvText(row.UserEmail), row.UserID, csvText(row.KeyName), key, csvText(row.Provider), csvText(row.Model), csvText(row.Status), usage}
		if member {
			values = append(values, csvMoney(row.ConsumedUSD))
		} else if row.Cost == nil {
			values = append(values, "", "", "", "", "", csvMoney(row.ConsumedUSD), "")
		} else {
			c := row.Cost
			metered, confirmed := c.MeteredUSD, c.TariffUSD
			zero := money.USD(0)
			if c.ActualSource == "free_provider" || c.ActualSource == "provider_failed_no_charge" {
				if metered == nil {
					metered = &zero
				}
				if confirmed == nil {
					confirmed = &zero
				}
			}
			units := ""
			if c.PackageUnits != nil {
				units = strconv.FormatFloat(*c.PackageUnits, 'f', -1, 64)
			}
			values = append(values, csvMoney(&c.ExpectedUSD), csvMoney(metered), csvMoney(confirmed), csvMoney(c.ActualUSD), units, csvMoney(row.ConsumedUSD), strconv.FormatFloat(float64(c.MarkupPercent)/100, 'f', 2, 64))
		}
		identitySource := "Current labels; historical identity unavailable"
		if row.IdentityHistorical {
			identitySource = "Captured at request time"
		}
		if commercial {
			at := ""
			if row.ProfitRecognizedAt != nil {
				at = row.ProfitRecognizedAt.UTC().Format(time.RFC3339Nano)
			}
			values = append(values, csvText(row.ProfitStatus), csvMoney(row.ProfitUSD), at)
		}
		values = append(values, identitySource)
		return writer.Write(values)
	})
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if err != nil {
		writeError(w, 503, "unavailable", "report export failed; retry")
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		writeError(w, 503, "unavailable", "report export failed")
		return
	}
	info, err := file.Stat()
	if err != nil {
		writeError(w, 503, "unavailable", "report export failed")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", `attachment; filename="generation-report.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, file)
}
