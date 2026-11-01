package references

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

type uploadPolicyContextKey struct{}
type uploadPolicyRepo struct {
	aacUploadRepository
	session UploadSession
	uses    int
}

func (r *uploadPolicyRepo) CreateUpload(_ context.Context, s UploadSession) error {
	r.session = s
	return nil
}
func (r *uploadPolicyRepo) UseUpload(ctx context.Context, _ string, _ bool, f func(context.Context, *UploadSession, Repository) error) error {
	r.uses++
	return f(context.WithValue(ctx, uploadPolicyContextKey{}, true), &r.session, r)
}
func (*uploadPolicyRepo) ExpiredUploads(context.Context) ([]ExpiredUpload, error) { return nil, nil }

type uploadPolicyStore struct {
	aacUploadStore
	starts, completes int
}

func (*uploadPolicyStore) PlanUpload(_ context.Context, id, kind, hash string, size int64) (*storage.PendingUpload, error) {
	return &storage.PendingUpload{Key: id, ContentType: kind, SHA256: hash, Size: size}, nil
}
func (s *uploadPolicyStore) StartUpload(_ context.Context, p *storage.PendingUpload) (storage.UploadTarget, error) {
	s.starts++
	p.UploadID = "fixture-upload"
	return storage.UploadTarget{}, nil
}
func (s *uploadPolicyStore) CompleteUpload(context.Context, storage.PendingUpload) (core.StoredMedia, error) {
	s.completes++
	return core.StoredMedia{}, errors.New("unexpected complete")
}
func (*uploadPolicyStore) AbortUpload(context.Context, storage.PendingUpload, bool) error { return nil }

func TestUploadLimitRequiresHostResolver(t *testing.T) {
	target := &referencevalidation.Target{Provider: "invented", Model: "300000000"}
	for _, tc := range []struct {
		name   string
		target *referencevalidation.Target
		limit  int64
		want   int64
		bad    bool
	}{
		{"nil target cannot extend", nil, HardMaxBytes, MaxBytes, false},
		{"authorized exact", target, HardMaxBytes, HardMaxBytes, false},
		{"above hard cap", target, HardMaxBytes + 1, 0, true},
		{"zero rejected", target, 0, 0, true},
		{"negative rejected", target, -1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ctx := WithUploadLimitResolver(context.Background(), func(context.Context, *referencevalidation.Target) (int64, error) { calls++; return tc.limit, nil })
			got, err := UploadLimit(ctx, tc.target)
			if got != tc.want || (err != nil) != tc.bad {
				t.Fatalf("got=%d err=%v", got, err)
			}
			if tc.target == nil && calls != 0 {
				t.Fatal("nil target consulted resolver")
			}
		})
	}
	if got, err := UploadLimit(context.Background(), target); err != nil || got != MaxBytes {
		t.Fatal("client target raised default", got, err)
	}
	sentinel := errors.New("model is hidden")
	ctx := WithUploadLimitResolver(context.Background(), func(context.Context, *referencevalidation.Target) (int64, error) { return 0, sentinel })
	if _, err := UploadLimit(ctx, target); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestDirectUploadTargetBoundariesAndCompletionReauthorization(t *testing.T) {
	target := &referencevalidation.Target{Provider: "kling", Model: "kling-4.0-omni-video", Options: core.MediaOptions{ReferenceMode: "video-reference"}}
	for _, tc := range []struct {
		name    string
		target  *referencevalidation.Target
		host    bool
		size    int64
		allowed bool
	}{
		{"ordinary exact", nil, false, MaxBytes, true},
		{"ordinary over", nil, false, MaxBytes + 1, false},
		{"target alone", target, false, MaxBytes + 1, false},
		{"reviewed decimal exact", target, true, HardMaxBytes, true},
		{"reviewed decimal over", target, true, HardMaxBytes + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &uploadPolicyRepo{}
			store := &uploadPolicyStore{}
			library := New(repo, store)
			ctx := context.Background()
			if tc.host {
				ctx = WithUploadLimitResolver(ctx, func(context.Context, *referencevalidation.Target) (int64, error) { return HardMaxBytes, nil })
			}
			_, err := library.BeginUploadWithValidation(ctx, UploadInput{Name: "synthetic.mp4", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: tc.size, Validation: tc.target}, nil)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t err=%v", tc.allowed, err)
			}
			if !tc.allowed && store.starts != 0 {
				t.Fatal("rejected admission started upload")
			}
			if !tc.allowed {
				return
			}
			if tc.host {
				calls := 0
				downgraded := WithUploadLimitResolver(context.Background(), func(tx context.Context, saved *referencevalidation.Target) (int64, error) {
					calls++
					if tx.Value(uploadPolicyContextKey{}) != true {
						t.Fatal("reauthorization outside owner transaction")
					}
					if saved.Model != target.Model {
						t.Fatal("saved target lost")
					}
					return MaxBytes, nil
				})
				_, err = library.FinishUploadWithValidation(downgraded, repo.session.Item.ID, nil)
				if !errors.Is(err, referencevalidation.ErrInvalid) || calls != 1 || store.completes != 0 {
					t.Fatalf("completion not reauthorized: %v calls=%d completes=%d", err, calls, store.completes)
				}
			}
		})
	}
}

func TestLocalUploadPolicyAndFormatCaps(t *testing.T) {
	target := &referencevalidation.Target{Provider: "fixture", Model: "reviewed"}
	data := testmedia.MP4(5000, 1000, 150)
	for _, limit := range []int64{int64(len(data) - 1), int64(len(data))} {
		repo := &aacUploadRepository{}
		store := &aacUploadStore{}
		ctx := WithUploadLimitResolver(context.Background(), func(context.Context, *referencevalidation.Target) (int64, error) { return limit, nil })
		calls := 0
		_, err := New(repo, store).UploadWithValidation(ctx, "synthetic.mp4", bytes.NewReader(data), target, func(context.Context, *referencevalidation.Target, []referencevalidation.File, bool) error {
			calls++
			return nil
		})
		if limit < int64(len(data)) {
			if !errors.Is(err, referencevalidation.ErrInvalid) || len(store.data) != 0 || calls != 0 {
				t.Fatalf("bounded reader admitted oversized data: %v", err)
			}
		} else if err != nil || calls != 1 {
			t.Fatalf("exact authorized data rejected: %v", err)
		}
	}
	ctx := WithUploadLimitResolver(context.Background(), func(context.Context, *referencevalidation.Target) (int64, error) { return HardMaxBytes, nil })
	for _, tc := range []struct {
		kind string
		size int64
	}{{"image/png", 30<<20 + 1}, {"audio/wave", 50<<20 + 1}, {"video/mp4", HardMaxBytes + 1}} {
		if err := checkUploadLimit(ctx, target, "synthetic", tc.kind, tc.size); !errors.Is(err, referencevalidation.ErrInvalid) {
			t.Fatal("format cap bypass", tc, err)
		}
	}
}

func TestCompletedUploadReplayReauthorizesSavedTarget(t *testing.T) {
	repo := &uploadPolicyRepo{}
	store := &uploadPolicyStore{}
	target := &referencevalidation.Target{Model: "reviewed"}
	repo.session = UploadSession{Validation: target, Item: Image{ID: "fixture", Name: "stored.mp4", ContentType: "video/mp4", SizeBytes: HardMaxBytes}, ResultID: "fixture", ExpiresAt: time.Now().Add(time.Hour)}
	calls := 0
	ctx := WithUploadLimitResolver(context.Background(), func(tx context.Context, saved *referencevalidation.Target) (int64, error) {
		calls++
		if tx.Value(uploadPolicyContextKey{}) != true || saved != target {
			t.Fatal("unscoped target")
		}
		return MaxBytes, nil
	})
	_, err := New(repo, store).FinishUploadWithValidation(ctx, "fixture", nil)
	if !errors.Is(err, referencevalidation.ErrInvalid) || calls != 1 || store.completes != 0 {
		t.Fatalf("replay bypassed current authorization: %v", err)
	}
}

// The padding is generated in memory at runtime; no large media fixture is kept
// in the repository. One byte beyond the ordinary ceiling exercises real owned
// inspection, checksum verification and provider-source staging.
func TestStoredExtendedVideoInspectionAndResourceStream(t *testing.T) {
	if testing.Short() {
		t.Skip("runtime-generated 200 MiB boundary fixture")
	}
	data := make([]byte, MaxBytes+1)
	header := testmedia.MP4(5000, 1000, 150)
	copy(data, header)
	binary.BigEndian.PutUint32(data[len(header):], uint32(len(data)-len(header)))
	copy(data[len(header)+4:], "free")
	sum := sha256.Sum256(data)
	item := Image{ID: uuid.NewString(), Name: "synthetic-boundary.mp4", ContentType: "video/mp4", SizeBytes: int64(len(data)), StorageKey: "fixture-extended", SHA256: hex.EncodeToString(sum[:])}
	repo := &aacUploadRepository{item: item}
	store := &aacUploadStore{data: data, kind: item.ContentType}
	library := New(repo, store)
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	file, err := library.Inspect(ctx, item.ID)
	if err != nil || file.Info.Size != item.SizeBytes || file.SHA256 != item.SHA256 {
		t.Fatalf("owned extended inspection: file=%+v err=%v", file, err)
	}
	if err = referencevalidation.ValidateFiles(core.MediaModel{}, []referencevalidation.File{file}, false); !errors.Is(err, referencevalidation.ErrInvalid) {
		t.Fatalf("stored extended video reused by ordinary model: %v", err)
	}
	manual := core.MediaModel{Manual: true, ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxBytes: HardMaxBytes, MaxBytesInclusive: true}}}
	if err = referencevalidation.ValidateFiles(manual, []referencevalidation.File{file}, false); !errors.Is(err, referencevalidation.ErrInvalid) {
		t.Fatalf("stored extended video reused by manual model: %v", err)
	}
	resolver := NewResolver(library, "fixture")
	request := core.ResourceSourceInput{ReferenceID: item.ID, SHA256: item.SHA256, ContentType: item.ContentType, MediaType: "video", SizeBytes: item.SizeBytes, URL: "https://169.254.169.254/not-followed"}
	if opened, e := resolver.OpenResourceSource(ctx, request); !errors.Is(e, ErrResourceSource) || opened.Reader != nil {
		t.Fatalf("default stream ceiling bypassed: %v", e)
	}
	ctx = core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: "actual-connection", OwnerID: "actual-provider-owner", Revision: "pinned-revision"})
	ctx = WithResourceSourceModel(ctx, "reviewed-video", core.MediaModel{ID: "reviewed-video", SupportsVideoInput: true, ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxBytes: HardMaxBytes, MaxBytesInclusive: true}}})
	source, err := resolver.OpenResourceSource(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, source.Reader)
	closeErr := source.Reader.Close()
	if err != nil || closeErr != nil || n != item.SizeBytes || source.SizeBytes != item.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
		t.Fatalf("staged bytes changed: n=%d err=%v close=%v", n, err, closeErr)
	}
}

func TestExtendedResourceReadProofCannotCrossHostScope(t *testing.T) {
	base := identity.WithOwnerID(context.Background(), "accepted-owner")
	scope := core.ProviderConnectionScope{ID: "actual-connection", OwnerID: "provider-owner", Revision: "revision-one"}
	base = core.WithProviderConnectionScope(base, scope)
	model := core.MediaModel{ID: "reviewed-operation", SupportsVideoInput: true, ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxBytes: HardMaxBytes, MaxBytesInclusive: true}}}
	proof := WithResourceSourceModel(base, model.ID, model)
	source := core.ResourceSourceInput{MediaType: "video", SizeBytes: MaxBytes + 1}
	if got := resourceSourceLimit(proof, source); got != HardMaxBytes {
		t.Fatal("host proof did not admit reviewed read", got)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"no proof", base},
		{"foreign owner", identity.WithOwnerID(proof, "foreign-owner")},
		{"different connection", core.WithProviderConnectionScope(proof, core.ProviderConnectionScope{ID: "other", OwnerID: scope.OwnerID, Revision: scope.Revision})},
		{"different revision", core.WithProviderConnectionScope(proof, core.ProviderConnectionScope{ID: scope.ID, OwnerID: scope.OwnerID, Revision: "revision-two"})},
		{"pinned revision changed", core.WithProviderRevision(proof, "revision-two")},
		{"wrong operation", WithResourceSourceModel(base, "other-operation", model)},
		{"manual model", func() context.Context { m := model; m.Manual = true; return WithResourceSourceModel(base, m.ID, m) }()},
		{"no actual scope", WithResourceSourceModel(identity.WithOwnerID(context.Background(), "accepted-owner"), model.ID, model)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resourceSourceLimit(tc.ctx, source); got != MaxBytes {
				t.Fatal("untrusted read proof extended ceiling", got)
			}
		})
	}
	source.MediaType = "audio"
	if got := resourceSourceLimit(proof, source); got != MaxBytes {
		t.Fatal("video read proof extended audio", got)
	}
}
