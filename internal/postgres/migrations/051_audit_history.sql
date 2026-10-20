-- +goose Up
-- Display identities are historical facts, not authorization sources. No foreign
-- keys here: removing a live object must not destroy its audit history.
CREATE TABLE audit_events (
 id BIGSERIAL PRIMARY KEY, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 schema_version INTEGER NOT NULL DEFAULT 1, scope TEXT NOT NULL DEFAULT 'system' CHECK(scope IN ('system','organization','personal')),
 organization_id TEXT NOT NULL DEFAULT '', organization_name TEXT NOT NULL DEFAULT '',
 actor_id TEXT NOT NULL DEFAULT '', actor_name TEXT NOT NULL DEFAULT '', actor_email TEXT NOT NULL DEFAULT '',
 actor_type TEXT NOT NULL DEFAULT 'service', calling_key_id TEXT NOT NULL DEFAULT '',
 action TEXT NOT NULL, object_type TEXT NOT NULL, object_id TEXT NOT NULL, object_name TEXT NOT NULL DEFAULT '',
 outcome TEXT NOT NULL DEFAULT 'success' CHECK(outcome IN ('success','denied','failed')),
 correlation_id TEXT NOT NULL DEFAULT '', details JSONB NOT NULL DEFAULT '{}',
 legacy_source TEXT NOT NULL DEFAULT '', legacy_id BIGINT
);
CREATE INDEX audit_time ON audit_events(created_at DESC,id DESC);
CREATE INDEX audit_org_time ON audit_events(organization_id,created_at DESC,id DESC);
CREATE INDEX audit_actor_time ON audit_events(actor_id,created_at DESC,id DESC);
CREATE INDEX audit_object_time ON audit_events(object_type,object_id,created_at DESC,id DESC);
CREATE INDEX audit_action_time ON audit_events(action,created_at DESC,id DESC);
CREATE TABLE revoked_key_history (
 key_id TEXT PRIMARY KEY, organization_id TEXT NOT NULL DEFAULT '',
 revoked_at TIMESTAMPTZ NOT NULL, snapshot JSONB NOT NULL
);
CREATE INDEX revoked_key_time ON revoked_key_history(organization_id,revoked_at DESC,key_id);

-- +goose StatementBegin
CREATE FUNCTION audit_redact(value JSONB) RETURNS JSONB LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result JSONB; k TEXT; v JSONB;
BEGIN
 IF jsonb_typeof(value)='object' THEN
  result='{}';
  FOR k,v IN SELECT * FROM jsonb_each(value) LOOP
   IF lower(k) ~ '(secret|cipher|password|token|authorization|prompt|instructions|webhook_url|signed_url|extra_body|raw)' OR lower(k) IN ('api_key','key','credentials') THEN
    CONTINUE;
   END IF;
   result=result || jsonb_build_object(k,audit_redact(v));
  END LOOP;
  RETURN result;
 ELSIF jsonb_typeof(value)='array' THEN
  SELECT COALESCE(jsonb_agg(audit_redact(x)),'[]') INTO result FROM jsonb_array_elements(value) x;
  RETURN result;
 END IF;
 RETURN value;
END $$;
-- +goose StatementEnd
INSERT INTO audit_events(created_at,scope,organization_id,actor_id,actor_name,actor_email,actor_type,action,object_type,object_id,details,legacy_source,legacy_id)
 SELECT * FROM (
 SELECT created_at,'system' AS scope,'' AS organization_id,COALESCE(substring(actor from '\[([0-9a-f-]{36})\]$'),''),actor,
 COALESCE(substring(actor from '<([^>]+)>'),''),CASE WHEN actor='Server command' THEN 'service' ELSE 'user' END,
 action,CASE WHEN action IN ('administrator_role','user_role','client_manager_role','administrator_bootstrap','administrator_key') THEN 'user' ELSE action END,target,
 audit_redact(details),'system_audit_log' AS legacy_source,id AS legacy_id FROM system_audit_log
 UNION ALL
 SELECT e.created_at,'organization',e.organization_id::text,e.actor_id::text,'','','user',e.action,
 CASE WHEN e.action LIKE 'key_%' THEN 'api_key' WHEN e.action LIKE 'invite_%' THEN 'invitation' WHEN e.action='member' THEN 'membership' ELSE 'organization' END,
 COALESCE(e.details->>'key_id',e.details->>'id',e.details->>'user_id',e.organization_id::text),
 audit_redact(e.details),'organization_events',e.id FROM organization_events e
 ) imported ORDER BY created_at,legacy_source,legacy_id;
-- Historic names were never stored; do not pass today's names off as past facts.
DROP TABLE system_audit_log;
DROP TABLE organization_events;

-- +goose StatementBegin
CREATE FUNCTION audit_record(org TEXT, actor TEXT, act TEXT, kind TEXT, obj TEXT, label TEXT, data JSONB, result TEXT DEFAULT 'success') RETURNS BIGINT LANGUAGE plpgsql AS $$
DECLARE who TEXT; uname TEXT=''; mail TEXT=''; company TEXT=''; event_id BIGINT;
BEGIN
 who=COALESCE(NULLIF(actor,''),NULLIF(current_setting('gateway.audit_actor',true),''),'');
 IF who <> '' THEN SELECT display_name,email INTO uname,mail FROM users WHERE id::text=who; END IF;
 IF COALESCE(org,'') <> '' THEN SELECT name INTO company FROM organizations WHERE id::text=org; END IF;
 IF COALESCE(label,'')='' THEN
  IF kind='user' THEN SELECT display_name INTO label FROM users WHERE id::text=obj;
  ELSIF kind='organization' THEN SELECT name INTO label FROM organizations WHERE id::text=obj;END IF;
 END IF;
 INSERT INTO audit_events(scope,organization_id,organization_name,actor_id,actor_name,actor_email,actor_type,calling_key_id,action,object_type,object_id,object_name,outcome,correlation_id,details)
 VALUES(CASE WHEN COALESCE(org,'')<>'' THEN 'organization' WHEN kind IN ('user','api_key','provider','storage') AND act NOT IN ('administrator_role','user_role','client_manager_role','administrator_bootstrap','administrator_key') AND obj NOT LIKE '00000000-0000-0000-0000-000000000001/%' THEN 'personal' ELSE 'system' END,COALESCE(org,''),COALESCE(company,''),who,COALESCE(uname,''),COALESCE(mail,''),
 CASE WHEN uname IS NULL OR who='' OR who='00000000-0000-0000-0000-000000000000' THEN 'service' ELSE 'user' END,
 COALESCE(current_setting('gateway.audit_key',true),''),act,kind,obj,COALESCE(label,''),result,
 COALESCE(current_setting('gateway.audit_correlation',true),''),audit_redact(data)) RETURNING id INTO event_id;
 RETURN event_id;
END $$;
-- +goose StatementEnd

-- Explicit per-table projection: never serialize credentials or arbitrary HTTP bodies.
-- +goose StatementBegin
CREATE FUNCTION audit_management_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE previous JSONB='{}'; following JSONB='{}'; oldrow JSONB='{}'; newrow JSONB='{}';
 fields TEXT[]; field TEXT; org TEXT=''; actor TEXT=''; obj TEXT; label TEXT; act TEXT=TG_ARGV[0]; secrets TEXT[];
BEGIN
 IF TG_OP<>'INSERT' THEN oldrow=to_jsonb(OLD); END IF;
 IF TG_OP<>'DELETE' THEN newrow=to_jsonb(NEW); END IF;
 fields=string_to_array(TG_ARGV[2],',');
 FOREACH field IN ARRAY fields LOOP
  IF oldrow->field IS DISTINCT FROM newrow->field THEN
   previous=previous || jsonb_build_object(field,oldrow->field);
   following=following || jsonb_build_object(field,newrow->field);
  END IF;
 END LOOP;
 secrets=string_to_array(COALESCE(TG_ARGV[3],''),',');
 FOREACH field IN ARRAY secrets LOOP
  IF field<>'' AND oldrow->field IS DISTINCT FROM newrow->field THEN
   -- Only state transitions; neither ciphertext nor credential fragments.
   following=following || jsonb_build_object(regexp_replace(field,'(credentials_ciphertext|secret_ciphertext|secret_cipher|token_hash|key_ciphertext|config_ciphertext|password_hash)$','credential') || '_change', CASE WHEN newrow->field IS NULL OR newrow->field='null'::jsonb THEN 'cleared' WHEN oldrow->field IS NULL OR oldrow->field='null'::jsonb THEN 'added' ELSE 'replaced' END);
  END IF;
 END LOOP;
 IF previous='{}' AND following='{}' THEN RETURN NULL; END IF;
 org=COALESCE(newrow->>'organization_id',oldrow->>'organization_id','');
 obj=COALESCE(newrow->>'id',oldrow->>'id',newrow->>'provider_id',oldrow->>'provider_id',newrow->>'provider',oldrow->>'provider','installation');
 label=COALESCE(newrow->>'name',oldrow->>'name',newrow->>'display_name',oldrow->>'display_name','');
 IF TG_TABLE_NAME='organizations' THEN org=obj; IF TG_OP='INSERT' THEN act='organization_created';ELSIF following ? 'client_manager_id' THEN act='organization_client_manager';END IF;END IF;
 IF TG_TABLE_NAME='organization_invites' THEN IF TG_OP='INSERT' THEN act='invite_created';ELSIF following ? 'accepted_by' THEN act='invite_accepted';ELSIF following ? 'revoked_at' THEN act='invite_revoked';END IF;END IF;
 IF TG_TABLE_NAME='organization_members' THEN obj=org || '/' || COALESCE(newrow->>'user_id',oldrow->>'user_id'); END IF;
 IF TG_TABLE_NAME IN ('provider_settings','storage_settings','storage_connections') THEN
  org=COALESCE(newrow->>'owner_id',oldrow->>'owner_id','');
  IF NOT EXISTS(SELECT 1 FROM organizations WHERE id::text=org) THEN org=''; END IF;
  obj=COALESCE(newrow->>'owner_id',oldrow->>'owner_id') || '/' || obj;
 END IF;
 IF TG_TABLE_NAME='users' THEN
  actor=COALESCE(NULLIF(current_setting('gateway.audit_actor',true),''),obj);
  IF TG_OP='INSERT' THEN act='account_created'; END IF;
 END IF;
 IF TG_TABLE_NAME='email_credentials' THEN actor=COALESCE(newrow->>'user_id',oldrow->>'user_id');obj=actor;END IF;
 IF TG_TABLE_NAME IN ('system_settings','organizations') AND TG_OP<>'INSERT' THEN
  IF following ? 'request_limits' THEN act='request_limits';
  ELSIF following ? 'markup_basis_points' OR following ? 'markup_exceptions' THEN act='markups';
  ELSIF following ? 'minimize_request_logging' THEN act='request_logging';END IF;
 END IF;
 PERFORM audit_record(org,actor,act,TG_ARGV[1],obj,label,jsonb_build_object('operation',lower(TG_OP),'before',previous,'after',following));
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_system_settings AFTER INSERT OR UPDATE ON system_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('settings','system','generation_ui,personal_providers,shared_resources,shared_storage_limit_bytes,allow_inline_references,markup_basis_points,markup_exceptions,request_limits','');
CREATE TRIGGER audit_organizations AFTER INSERT OR UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION audit_management_change('settings','organization','name,credit_limit_micros,default_weekly_limit_micros,minimize_request_logging,client_manager_id,billing_profile,markup_basis_points,markup_exceptions,request_limits','');
CREATE TRIGGER audit_organization_members AFTER INSERT OR UPDATE ON organization_members FOR EACH ROW EXECUTE FUNCTION audit_management_change('member','membership','role,weekly_limit_micros,removed_at','');
CREATE TRIGGER audit_organization_invites AFTER INSERT OR UPDATE ON organization_invites FOR EACH ROW EXECUTE FUNCTION audit_management_change('invitation','invitation','expires_at,accepted_by,revoked_at','');
CREATE TRIGGER audit_provider_settings AFTER INSERT OR UPDATE ON provider_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('provider_settings','provider','name,enabled,archived,credential_source','credentials_ciphertext');
CREATE TRIGGER audit_storage_settings AFTER INSERT OR UPDATE ON storage_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('storage_settings','storage','driver,connection_id','');
CREATE TRIGGER audit_storage_connections AFTER INSERT OR UPDATE ON storage_connections FOR EACH ROW EXECUTE FUNCTION audit_management_change('storage_connection','storage','id','config_ciphertext');
CREATE TRIGGER audit_model_settings AFTER INSERT OR UPDATE ON model_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('model_settings','model','public_id,enabled_by_default','');
CREATE TRIGGER audit_email_delivery_settings AFTER INSERT OR UPDATE ON email_delivery_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('email_delivery','email_provider','provider,sender','key_ciphertext');
CREATE TRIGGER audit_login_providers AFTER INSERT OR UPDATE ON login_providers FOR EACH ROW EXECUTE FUNCTION audit_management_change('login_provider','login_provider','provider,enabled,connected,client_id','secret_ciphertext');
CREATE TRIGGER audit_payment_providers AFTER INSERT OR UPDATE ON payment_providers FOR EACH ROW EXECUTE FUNCTION audit_management_change('payment_provider','payment_provider','kind,name,enabled,live,account_id,fee_mode','secret_cipher,token_hash');
CREATE TRIGGER audit_payment_notification_settings AFTER INSERT OR UPDATE ON payment_notification_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('payment_notifications','system','critical_emails,success_emails','');
CREATE TRIGGER audit_users AFTER INSERT OR UPDATE ON users FOR EACH ROW EXECUTE FUNCTION audit_management_change('profile','user','email,display_name,theme,report_columns','');
CREATE TRIGGER audit_email_credentials AFTER INSERT OR UPDATE ON email_credentials FOR EACH ROW EXECUTE FUNCTION audit_management_change('password_changed','user','','password_hash');
CREATE TRIGGER audit_organization_credits AFTER INSERT OR UPDATE ON organization_credits FOR EACH ROW EXECUTE FUNCTION audit_management_change('credit','credit','amount_micros,kind,external_id,payment_id,payment_revision','');

-- Old revoked rows retain only facts which were actually known; historical spend
-- and the revoking actor cannot be reconstructed and are deliberately absent.
INSERT INTO revoked_key_history(key_id,organization_id,revoked_at,snapshot)
 SELECT k.id::text,COALESCE(k.organization_id::text,''),k.revoked_at,
 jsonb_build_object('id',k.id,'name',k.name,'prefix',k.prefix,'organization_id',k.organization_id,
 'owner_id',k.user_id,'created_by',k.created_by,'created_at',k.created_at,'revoked_at',k.revoked_at,
 'scopes',k.scopes,'history_complete',false,'budget',NULL)
 FROM user_api_keys k WHERE k.revoked_at IS NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION audit_key_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE company TEXT=''; owner_mail TEXT=''; creator_mail TEXT=''; actor TEXT=''; who_name TEXT='';who_mail TEXT=''; spent BIGINT; pending BIGINT; unknown BIGINT; snap JSONB;
BEGIN
 IF TG_OP='UPDATE' AND (OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL) THEN RETURN NULL;END IF;
 IF NEW.organization_id IS NOT NULL THEN SELECT name INTO company FROM organizations WHERE id=NEW.organization_id;END IF;
 SELECT email INTO owner_mail FROM users WHERE id=NEW.user_id;
 SELECT email INTO creator_mail FROM users WHERE id=NEW.created_by;
 actor=COALESCE(NULLIF(current_setting('gateway.audit_actor',true),''),CASE WHEN TG_OP='INSERT' THEN NEW.created_by::text ELSE '' END);
 IF TG_OP='INSERT' THEN
  PERFORM audit_record(NEW.organization_id::text,actor,'key_created','api_key',NEW.id::text,NEW.name,
   jsonb_build_object('after',jsonb_build_object('name',NEW.name,'owner_id',NEW.user_id,'owner_email',owner_mail,'scopes',NEW.scopes,'expires_at',NEW.expires_at,'resource_mode',NEW.resource_mode,'storage_mode',NEW.storage_mode)));
  RETURN NULL;
 END IF;
 SELECT display_name,email INTO who_name,who_mail FROM users WHERE id::text=actor;
 SELECT COALESCE(SUM(CEIL((CASE WHEN status IN ('queued','running') THEN 0::numeric ELSE COALESCE(
 CASE WHEN cost->>'actual_source' IN ('free_provider','provider_failed_no_charge') THEN 0::numeric END,
 (cost->>'tariff_usd')::numeric, (cost->>'metered_usd')::numeric,
 CASE WHEN provider_usage->>'currency'='USD' AND jsonb_typeof(provider_usage->'estimated_cost')='number'
 THEN GREATEST(0,(provider_usage->>'estimated_cost')::numeric) END) END)*(1+COALESCE((cost->>'markup_percent')::numeric,0)/100)*1000000)),0)::bigint,
 COUNT(*) FILTER(WHERE status IN ('queued','running')),
 COUNT(*) FILTER(WHERE status NOT IN ('queued','running') AND CEIL((CASE WHEN status IN ('queued','running') THEN 0::numeric ELSE COALESCE(
 CASE WHEN cost->>'actual_source' IN ('free_provider','provider_failed_no_charge') THEN 0::numeric END,
 (cost->>'tariff_usd')::numeric, (cost->>'metered_usd')::numeric,
 CASE WHEN provider_usage->>'currency'='USD' AND jsonb_typeof(provider_usage->'estimated_cost')='number'
 THEN GREATEST(0,(provider_usage->>'estimated_cost')::numeric) END) END)*(1+COALESCE((cost->>'markup_percent')::numeric,0)/100)*1000000) IS NULL)
 INTO spent,pending,unknown FROM tasks WHERE api_key_id=NEW.id;
 snap=jsonb_build_object('id',NEW.id,'name',NEW.name,'prefix',NEW.prefix,'organization_id',NEW.organization_id,
 'organization_name',COALESCE(company,''),'owner_id',NEW.user_id,'owner_email',owner_mail,'created_by',NEW.created_by,
 'creator_email',creator_mail,'created_at',NEW.created_at,'revoked_at',NEW.revoked_at,'revoked_by',actor,
 'revocation_reason',NULLIF(current_setting('gateway.audit_reason',true),''),'correlation_id',COALESCE(current_setting('gateway.audit_correlation',true),''),
 'expires_at',NEW.expires_at,'resource_mode',NEW.resource_mode,'storage_mode',NEW.storage_mode,
 'revoker_name',COALESCE(who_name,''),'revoker_email',COALESCE(who_mail,''),'scopes',NEW.scopes,
 'spent_micros',spent,'pending_requests',pending,'unknown_cost_requests',unknown,'budget',NULL,'history_complete',true);
 INSERT INTO revoked_key_history(key_id,organization_id,revoked_at,snapshot) VALUES(NEW.id::text,COALESCE(NEW.organization_id::text,''),NEW.revoked_at,snap);
 PERFORM audit_record(NEW.organization_id::text,actor,'key_revoked','api_key',NEW.id::text,NEW.name,jsonb_build_object('after',snap));
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_key AFTER INSERT OR UPDATE OF revoked_at ON user_api_keys FOR EACH ROW EXECUTE FUNCTION audit_key_change();

ALTER TABLE tasks ADD COLUMN identity_snapshot JSONB;
-- Existing requests have no truthful acceptance-time identities. Leave NULL and
-- explicitly label current-directory fallbacks in the report response.
-- +goose StatementBegin
CREATE FUNCTION task_identity_snapshot() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE who TEXT; mail TEXT; uname TEXT; company TEXT; alias TEXT; issuer TEXT; pname TEXT;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.identity_snapshot IS DISTINCT FROM OLD.identity_snapshot THEN RAISE EXCEPTION 'request identity is immutable';END IF;
  RETURN NEW;
 END IF;
 who=COALESCE(NEW.actor_id,NEW.owner_id)::text;
 SELECT email,display_name INTO mail,uname FROM users WHERE id::text=who;
 SELECT name INTO company FROM organizations WHERE id=NEW.owner_id;
 SELECT name,created_by::text INTO alias,issuer FROM user_api_keys WHERE id=NEW.api_key_id;
 SELECT name INTO pname FROM provider_settings WHERE owner_id=NEW.provider_owner_id AND provider_id=NEW.provider;
 NEW.identity_snapshot=jsonb_build_object('actor_id',who,'actor_email',COALESCE(mail,''),'actor_name',COALESCE(uname,''),
 'organization_id',CASE WHEN company IS NULL THEN '' ELSE NEW.owner_id::text END,'organization_name',COALESCE(company,''),
 'api_key_id',NEW.api_key_id,'api_key_name',COALESCE(alias,CASE WHEN NEW.api_key_id IS NULL THEN 'Console' ELSE 'Unknown key' END),
 'key_created_by',issuer,'provider_id',NEW.provider,'provider_name',COALESCE(pname,NEW.provider),'model',NEW.model);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER task_identity BEFORE INSERT OR UPDATE OF identity_snapshot ON tasks FOR EACH ROW EXECUTE FUNCTION task_identity_snapshot();
-- +goose StatementBegin
CREATE FUNCTION audit_immutable() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'audit history is append-only';END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_events FOR EACH STATEMENT EXECUTE FUNCTION audit_immutable();
CREATE TRIGGER revoked_history_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON revoked_key_history FOR EACH STATEMENT EXECUTE FUNCTION audit_immutable();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to roll back immutable audit history';END $$;
-- +goose StatementEnd
