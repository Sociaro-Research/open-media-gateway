// Package catalogsync reconciles versioned public model evidence with reviewed
// adapter contracts. Observations never become executable protocols by inference.
package catalogsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

var ErrSettings = errors.New("invalid catalogue synchronization settings")
var ErrBusy = errors.New("a catalogue synchronization is already running")

type Settings struct {
	Timezone string `json:"timezone"`
	Email    string `json:"email"`
	Version  int64  `json:"version"`
}

func (s Settings) Validate() error {
	if _, err := time.LoadLocation(s.Timezone); err != nil || s.Version < 1 || s.Timezone == "" || s.Timezone == "Local" {
		return ErrSettings
	}
	if s.Email != "" {
		a, e := mail.ParseAddress(s.Email)
		if e != nil || a.Address != s.Email || len(s.Email) > 254 || strings.ContainsAny(s.Email, "\r\n") {
			return ErrSettings
		}
	}
	return nil
}

type Source struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Parser string `json:"parser"`
}
type Schedule struct {
	Provider  string     `json:"provider"`
	Enabled   bool       `json:"enabled"`
	Time      string     `json:"time"`
	Sources   []Source   `json:"sources"`
	Version   int64      `json:"version"`
	NextCheck *time.Time `json:"next_check,omitempty"`
	LastCheck *time.Time `json:"last_check,omitempty"`
	Requested bool       `json:"requested"`
}

func (s Schedule) Validate() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`).MatchString(s.Provider) || s.Version < 1 {
		return ErrSettings
	}
	if _, err := time.Parse("15:04", s.Time); err != nil || len(s.Time) != 5 {
		return ErrSettings
	}
	if s.Sources == nil || len(s.Sources) > 30 {
		return ErrSettings
	}
	ids := map[string]bool{}
	for _, src := range s.Sources {
		u, e := url.Parse(src.URL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(src.URL) > 2048 || src.ID == "" || strings.HasPrefix(src.ID, "_") || len(src.ID) > 100 || ids[src.ID] || !SupportedParser(src.Parser) {
			return ErrSettings
		}
		ids[src.ID] = true
	}
	return nil
}

// NextDaily uses one local calendar occurrence. A repeated hour runs once at
// its first occurrence; a skipped hour runs at the first valid time after it.
func NextDaily(now time.Time, clock, zone string) time.Time {
	loc, _ := time.LoadLocation(zone)
	if loc == nil {
		loc = time.UTC
	}
	t, _ := time.Parse("15:04", clock)
	local := now.In(loc)
	date := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, time.UTC)
	for day := 0; day < 4; day++ {
		target := date.AddDate(0, 0, day)
		start := time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, loc).Add(-4 * time.Hour)
		for candidate := start; candidate.Before(start.Add(36 * time.Hour)); candidate = candidate.Add(time.Minute) {
			wall := candidate.In(loc)
			if wall.Year() != target.Year() || wall.Month() != target.Month() || wall.Day() != target.Day() {
				continue
			}
			if wall.Hour()*60+wall.Minute() < t.Hour()*60+t.Minute() {
				continue
			}
			// Do not select the second instance of a repeated hour after the first ran.
			if candidate.After(now) {
				return candidate.UTC()
			}
			break
		}
	}
	return now.AddDate(0, 0, 1).UTC()
}

type Rate struct {
	Source      string                  `json:"source,omitempty"`
	Region      string                  `json:"region"`
	Resolution  string                  `json:"resolution,omitempty"`
	Quality     string                  `json:"quality,omitempty"`
	Audio       *bool                   `json:"audio,omitempty"`
	VideoInput  *bool                   `json:"video_input,omitempty"`
	InputImages *ImageInputTariff       `json:"input_images,omitempty"`
	Unit        string                  `json:"unit"`
	Per         int64                   `json:"per"`
	Price       money.USD               `json:"price"`
	Components  []core.PricingComponent `json:"components,omitempty"`
}

// ImageInputTariff is a reviewed fixed charge for submitted reference images.
// The resulting amount is frozen in the accepted task's PricingSnapshot.
type ImageInputTariff struct {
	Free  int       `json:"free"`
	Price money.USD `json:"price"`
}
type Entry struct {
	ID              string              `json:"id"`
	Inactive        bool                `json:"inactive,omitempty"`
	ExcludedReason  string              `json:"excluded_reason,omitempty"`
	DiscoveryStatus string              `json:"discovery_status,omitempty"`
	Metadata        *core.ModelMetadata `json:"metadata,omitempty"`
	Contract        *core.MediaModel    `json:"contract,omitempty"`
	Rates           []Rate              `json:"rates,omitempty"`
}
type Observation struct {
	Source      Source          `json:"source"`
	Hash        string          `json:"hash"`
	FetchedHash string          `json:"fetched_hash,omitempty"`
	Entries     []Entry         `json:"entries"`
	Review      *DocumentReview `json:"review,omitempty"`
	// Text evidence is kept even when no safe structured interpretation exists.
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// DocumentReview acknowledges only one exact public document after a code review.
// A later content hash, different URL or failed fetch must require review again.
type DocumentReview struct {
	Hash   string `json:"hash"`
	Date   string `json:"date"`
	Reason string `json:"reason"`
}
type Change struct {
	Model  string          `json:"model"`
	Field  string          `json:"field"`
	Source string          `json:"source"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}
type Warning struct {
	Key         string          `json:"key"`
	Model       string          `json:"model,omitempty"`
	Source      string          `json:"source"`
	Reason      string          `json:"reason"`
	FirstSeen   time.Time       `json:"first_seen"`
	LastSeen    time.Time       `json:"last_seen"`
	Occurrences int             `json:"occurrences"`
	ResolvedAt  *time.Time      `json:"resolved_at,omitempty"`
	Evidence    json.RawMessage `json:"evidence,omitempty"`
}
type State struct {
	Version      int64                  `json:"version"`
	Models       map[string]Entry       `json:"models"`
	Observations map[string]Observation `json:"observations"`
	Warnings     []Warning              `json:"warnings"`
}
type Run struct {
	ID               string        `json:"id"`
	Provider         string        `json:"provider"`
	StartedAt        time.Time     `json:"started_at"`
	CompletedAt      time.Time     `json:"completed_at"`
	Status           string        `json:"status"`
	Version          int64         `json:"version"`
	Changes          []Change      `json:"changes"`
	NewWarnings      []Warning     `json:"new_warnings"`
	OpenWarnings     []Warning     `json:"open_warnings"`
	ResolvedWarnings []Warning     `json:"resolved_warnings"`
	Sources          []Observation `json:"sources"`
	EmailStatus      string        `json:"email_status"`
}
type Capture struct {
	Models          []core.MediaModel
	DocumentReviews map[string]DocumentReview // Exact source URL -> reviewed content hash.
	TariffAliases   map[string]string         // Reviewed exact IDs only; never inferred from source names.
	// Reviewed protocols can be documented in other regions without being
	// available through the currently configured account's discovery API.
	Reviewed []core.MediaModel
	// Inactive models are reviewed historical records, never executable models.
	Inactive []core.MediaModel
	Excluded []Exclusion
	IDs      []string
}

// Exclusion is a reviewed scope decision, retained in versions and change logs.
// Exact IDs only: a future unknown model still requires an adapter review.
type Exclusion struct {
	ID       string
	Reason   string
	Metadata *core.ModelMetadata
}
type Provider interface {
	CaptureCatalog(context.Context, string) (Capture, error)
	CheckCatalogRate(context.Context, string, core.MediaModel, Rate) error
}
type Repository interface {
	Settings(context.Context) (Settings, error)
	SaveSettings(context.Context, string, Settings) (Settings, error)
	Schedules(context.Context) ([]Schedule, error)
	SaveSchedule(context.Context, string, Schedule) (Schedule, error)
	EnsureSchedule(context.Context, Schedule) error
	Request(context.Context, string, string) error
	State(context.Context, string) (State, error)
	Commit(context.Context, Schedule, State, Run, time.Time) error
	Runs(context.Context, string, int, int) ([]Run, error)
	Versions(context.Context, string, int, int) ([]State, error)
	Document(context.Context, string) (string, error)
	WithLock(context.Context, func(context.Context) error) (bool, error)
	PendingEmails(context.Context) ([]Run, error)
	EmailResult(context.Context, string, string) error
}
type Mailer interface {
	SendCatalogReport(context.Context, string, string, string, string, string) error
}
