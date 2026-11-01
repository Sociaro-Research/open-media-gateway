package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

// This is the schema-80 UPDATE used by the previously deployed repository. A
// normal Save replaces draft only; publish supplies the same new JSON twice.
const templateLegacyRecoveryUpdate = `UPDATE generation_templates SET version=version+1,revision=$3,draft=$4,published=$5,updated_at=now(),removed_at=CASE WHEN $6 THEN now() ELSE NULL END WHERE id=$1 AND version=$2`

type templateRecoveryState struct {
	Draft     json.RawMessage `json:"draft"`
	Published json.RawMessage `json:"published"`
	Version   int64           `json:"version"`
	Revision  int64           `json:"revision"`
}

func TestTemplateRecoveryRetainsRollbackWindowDrafts(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	move := func(version int64) {
		t.Helper()
		var err error
		if version == 80 {
			err = goose.DownTo(db, "migrations", 80)
		} else {
			err = goose.UpTo(db, "migrations", 81)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) sql.Result {
		t.Helper()
		r, e := db.Exec(query, args...)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	scalar := func(query string, args ...any) string {
		t.Helper()
		var v string
		if e := db.QueryRow(query, args...).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	if err := goose.UpTo(db, "migrations", 80); err != nil {
		t.Fatal(err)
	}
	user, key, task, image, audio := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,email,display_name) VALUES($1,'rollback-cycle@example.test','Synthetic')`, user)
	exec(`INSERT INTO user_api_keys(id,user_id,created_by,name,prefix,token_hash,scopes) VALUES($1,$2,$2,'Synthetic key','fixture',decode('012345','hex'),'["generations:write"]')`, key, user)
	exec(`INSERT INTO provider_settings(owner_id,provider_id,enabled,credentials_ciphertext,credential_source) VALUES($1,'fixture',true,decode('aabbcc','hex'),'console')`, user)
	exec(`UPDATE system_settings SET generation_ui=false,version=99`)
	exec(`INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,template_published) VALUES($1,$2,'synthetic.png','image/png',64,'fixture/image',true),($1,$3,'synthetic.wav','audio/wav',64,'fixture/audio',true)`, templates.LibraryOwnerID, image, audio)
	exec(`INSERT INTO tasks(id,owner_id,provider_owner_id,storage_owner_id,actor_id,api_key_id,status,provider,model,prompt,request_options,template_snapshot) VALUES($1,$2,$2,$2,$2,$3,'running','mock','fixture','Immutable admitted prompt','{"duration":5}','{"prompt":"Immutable admitted prompt","revision":4}')`, task, user, key)
	fingerprint := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, table := range []string{"users", "user_api_keys", "provider_settings", "system_settings", "reference_images", "tasks"} {
			out[table] = scalar(`SELECT md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'')) FROM ` + table + ` t`)
		}
		return out
	}
	protected := fingerprint()
	definition := func(prompt string) []byte {
		t.Helper()
		raw, e := json.Marshal(templates.Definition{Title: "Recovery fixture", MediaType: "video", Provider: "mock", Model: "fixture", Prompt: prompt, CoverID: image, OverrideSoundID: audio, Slots: []templates.Slot{{ID: "image:1", Type: "image", Index: 1, AssetID: image}}})
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	a, b, c, removed := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ids := []string{a, b, c, removed}
	exec(`INSERT INTO generation_templates(id,version,revision,draft,published) VALUES($1,9,4,$2,$3),($4,7,7,$3,$3),($5,3,0,$2,NULL),($6,4,2,$2,NULL)`, a, definition("Unvalidated original draft"), definition("Visible original"), b, c, removed)
	exec(`UPDATE generation_templates SET removed_at='2026-01-01' WHERE id=$1`, removed)
	removedAt := scalar(`SELECT removed_at::text FROM generation_templates WHERE id=$1`, removed)
	legacy := func(id string) templateRecoveryState {
		t.Helper()
		var s templateRecoveryState
		raw := scalar(`SELECT jsonb_build_object('draft',draft,'published',published,'version',version,'revision',revision)::text FROM generation_templates WHERE id=$1`, id)
		if e := json.Unmarshal([]byte(raw), &s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	bytesOf := func(s templateRecoveryState) []byte {
		t.Helper()
		raw, e := json.Marshal(s)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	preserved := func(id string, s templateRecoveryState) bool {
		t.Helper()
		var row struct {
			Legacy json.RawMessage   `json:"legacy_state"`
			States []json.RawMessage `json:"recovery_states"`
		}
		raw := scalar(`SELECT to_jsonb(t)::text FROM generation_templates t WHERE id=$1`, id)
		if e := json.Unmarshal([]byte(raw), &row); e != nil {
			t.Fatal(e)
		}
		want := bytesOf(s)
		if templateJSONEqual(row.Legacy, want) {
			return true
		}
		for _, state := range row.States {
			if templateJSONEqual(state, want) {
				return true
			}
		}
		return false
	}
	retained := map[string][]templateRecoveryState{}
	originals := map[string][]byte{}
	for _, id := range ids {
		retained[id] = append(retained[id], legacy(id))
	}
	move(81)
	for _, id := range ids {
		originals[id] = []byte(scalar(`SELECT legacy_state::text FROM generation_templates WHERE id=$1`, id))
	}
	repo := NewTemplateRepository(db)
	defer repo.Close()
	current, e := repo.Get(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	if current.Version != 10 || current.Revision != 4 {
		t.Fatal("divergent initial token transition", current)
	}
	if v := scalar(`SELECT version::text FROM generation_templates WHERE id=$1`, b); v != "7" {
		t.Fatal("matching initial edit token changed", v)
	}
	current.Draft.Prompt = "Approved current content on81"
	if _, e = repo.Save(context.Background(), current, current.Version, ""); e != nil {
		t.Fatal(e)
	}
	created := uuid.NewString()
	ids = append(ids, created)
	exec(`INSERT INTO generation_templates(id,content,is_published,version,revision) VALUES($1,$2,true,2,2)`, created, definition("Created on81"))
	counts := func() string {
		t.Helper()
		return scalar(`SELECT COALESCE(jsonb_object_agg(id::text,jsonb_array_length(COALESCE(to_jsonb(t)->'recovery_states','[]'::jsonb)))::text,'{}') FROM generation_templates t`)
	}
	oldSave := func(id string, draft []byte, published any, version, revision int64) int64 {
		t.Helper()
		r := exec(templateLegacyRecoveryUpdate, id, version, revision, draft, published, false)
		n, e := r.RowsAffected()
		if e != nil {
			t.Fatal(e)
		}
		return n
	}
	for cycle := 0; cycle < 3; cycle++ {
		// Down must retain the current81 state before old code can edit it.
		for _, id := range ids {
			raw := scalar(`SELECT jsonb_build_object('draft',content,'published',CASE WHEN is_published THEN content ELSE NULL END,'version',version,'revision',revision)::text FROM generation_templates WHERE id=$1`, id)
			var s templateRecoveryState
			if e := json.Unmarshal([]byte(raw), &s); e != nil {
				t.Fatal(e)
			}
			retained[id] = append(retained[id], s)
		}
		move(80)
		for _, id := range ids {
			state := legacy(id)
			if !templateJSONEqual(bytesOf(state), bytesOf(retained[id][len(retained[id])-1])) {
				t.Fatal("Down changed current aliases", cycle, id)
			}
		}
		// The real old SELECT and UPDATE/CAS shapes must keep working.
		_ = scalar(`SELECT count(*)::text FROM (SELECT id,version,revision,draft,published,updated_at FROM generation_templates WHERE removed_at IS NULL AND published IS NOT NULL) s`)
		state := legacy(a)
		next := definition(fmt.Sprintf("New unvalidated rollback draft %d", cycle))
		if oldSave(a, next, []byte(state.Published), state.Version, state.Revision) != 1 {
			t.Fatal("old Save failed")
		}
		if oldSave(a, definition("Stale edit must not persist"), []byte(state.Published), state.Version, state.Revision) != 0 {
			t.Fatal("stale old CAS accepted")
		}
		state = legacy(c)
		if oldSave(c, definition(fmt.Sprintf("Off rollback edit %d", cycle)), nil, state.Version, state.Revision) != 1 {
			t.Fatal("old off Save failed")
		}
		state = legacy(b)
		if cycle%2 == 0 {
			next = definition(fmt.Sprintf("Explicit approved old publish %d", cycle))
			oldSave(b, next, next, state.Version, state.Version+1)
		} else {
			oldSave(b, []byte(state.Draft), nil, state.Version, state.Revision)
		}
		if cycle == 0 {
			newID := uuid.NewString()
			ids = append(ids, newID)
			exec(`INSERT INTO generation_templates(id,draft) VALUES($1,$2)`, newID, definition("Created under old schema"))
		}
		expected := map[string]templateRecoveryState{}
		for _, id := range ids {
			expected[id] = legacy(id)
			retained[id] = append(retained[id], expected[id])
		}
		move(81)
		for _, id := range ids {
			if !preserved(id, expected[id]) {
				t.Fatalf("rollback-window state lost after reupgrade: cycle=%d id=%s", cycle, id)
			}
			for _, prior := range retained[id] {
				if !preserved(id, prior) {
					t.Fatalf("previous recovery evidence lost: cycle=%d id=%s", cycle, id)
				}
			}
			chosen, published := expected[id].Draft, false
			if string(expected[id].Published) != "null" {
				chosen, published = expected[id].Published, true
			}
			if !templateJSONEqual([]byte(scalar(`SELECT content::text FROM generation_templates WHERE id=$1`, id)), chosen) {
				t.Fatal("unvalidated state activated or current content lost", cycle, id)
			}
			if value := scalar(`SELECT is_published::text FROM generation_templates WHERE id=$1`, id); value != fmt.Sprint(published) {
				t.Fatal("visibility changed", cycle, id)
			}
			if original, ok := originals[id]; ok && !templateJSONEqual(original, []byte(scalar(`SELECT legacy_state::text FROM generation_templates WHERE id=$1`, id))) {
				t.Fatal("original legacy archive mutated", id)
			}
		}
		// First Down records any just-canonicalized current state. Unchanged repeated
		// Up/Down cycles must not append duplicates or change tokens/content.
		move(80)
		baselineCount := counts()
		stable := map[string][]byte{}
		for _, id := range ids {
			stable[id] = bytesOf(legacy(id))
		}
		move(81)
		if counts() != baselineCount {
			t.Fatal("unchanged Up duplicated recovery states")
		}
		move(80)
		if counts() != baselineCount {
			t.Fatal("unchanged Down duplicated recovery states")
		}
		for _, id := range ids {
			if !templateJSONEqual(stable[id], bytesOf(legacy(id))) {
				t.Fatal("unchanged cycle changed current content or tokens")
			}
		}
		move(81)
		if counts() != baselineCount {
			t.Fatal("repeated unchanged Up duplicated recovery states")
		}
	}
	if scalar(`SELECT removed_at::text FROM generation_templates WHERE id=$1`, removed) != removedAt {
		t.Fatal("removed row metadata changed")
	}
	for table, want := range protected {
		if got := fingerprint()[table]; got != want {
			t.Fatal("unrelated synthetic data changed", table)
		}
	}
	if scalar(`SELECT bool_and(n=distinct_n)::text FROM (SELECT (SELECT count(*) FROM jsonb_array_elements(recovery_states)) n,(SELECT count(DISTINCT s) FROM jsonb_array_elements(recovery_states) s) distinct_n FROM generation_templates) t`) != "true" {
		t.Fatal("duplicate recovery snapshots")
	}
}
