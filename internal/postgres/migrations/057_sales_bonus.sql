-- +goose Up
ALTER TABLE sales_permission_grants DROP CONSTRAINT sales_permission_grants_permission_check;
ALTER TABLE sales_permission_grants ADD CONSTRAINT sales_permission_grants_permission_check CHECK(permission IN ('pricing','models','topup','bonuses'));
CREATE TABLE sales_bonus_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id), manager_percent INTEGER NOT NULL DEFAULT 500,
 supervisor_percent INTEGER NOT NULL DEFAULT 100, version BIGINT NOT NULL DEFAULT 1,
 starts_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(manager_percent>=0 AND supervisor_percent>=0 AND manager_percent+supervisor_percent<=10000)
);
INSERT INTO sales_bonus_settings(id) VALUES(TRUE);
CREATE TRIGGER audit_sales_bonus_settings AFTER UPDATE ON sales_bonus_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('sales_bonus_settings','system','manager_percent,supervisor_percent','');
CREATE TABLE generation_profits (
 task_id UUID PRIMARY KEY REFERENCES tasks(id), organization_id UUID NOT NULL REFERENCES organizations(id),
 state TEXT NOT NULL CHECK(state IN ('awaiting_billing','awaiting_cost','recognized')),
 revenue_micros BIGINT, cost_micros BIGINT, profit_micros BIGINT,
 manager_id UUID REFERENCES users(id), supervisor_id UUID REFERENCES users(id),
 manager_percent INTEGER NOT NULL DEFAULT 0, supervisor_percent INTEGER NOT NULL DEFAULT 0,
 manager_bonus_micros BIGINT NOT NULL DEFAULT 0, supervisor_bonus_micros BIGINT NOT NULL DEFAULT 0,
 revision BIGINT NOT NULL DEFAULT 0, recognized_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX generation_profits_org ON generation_profits(organization_id);
CREATE TABLE sales_bonus_entries (
 id BIGSERIAL PRIMARY KEY, employee_id UUID NOT NULL REFERENCES users(id),
 task_id UUID REFERENCES tasks(id), revision BIGINT, beneficiary TEXT,
 amount_micros BIGINT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('accrual','correction','payout')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), actor_id UUID REFERENCES users(id),
 paid_at DATE, method TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', external_id UUID,
 CHECK((kind='payout' AND amount_micros<0 AND paid_at IS NOT NULL AND method<>'' AND external_id IS NOT NULL AND actor_id IS NOT NULL AND task_id IS NULL) OR (kind<>'payout' AND task_id IS NOT NULL AND revision IS NOT NULL AND beneficiary IN ('manager','supervisor'))),
 UNIQUE(task_id,revision,beneficiary), UNIQUE(actor_id,external_id)
);
CREATE INDEX sales_bonus_employee ON sales_bonus_entries(employee_id,id DESC);
CREATE TRIGGER sales_bonus_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON sales_bonus_entries FOR EACH STATEMENT EXECUTE FUNCTION audit_immutable();
CREATE TRIGGER audit_sales_bonus_entries AFTER INSERT ON sales_bonus_entries FOR EACH ROW EXECUTE FUNCTION audit_management_change('sales_bonus','sales_bonus','employee_id,task_id,revision,beneficiary,amount_micros,kind,paid_at,method,reason,external_id','');
-- +goose StatementBegin
CREATE FUNCTION recognize_generation_profit() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE cfg sales_bonus_settings%ROWTYPE; previous generation_profits%ROWTYPE;
 company UUID; manager UUID; supervisor UUID; revenue BIGINT; expense BIGINT; profit BIGINT;
 state TEXT='awaiting_cost'; base NUMERIC; next_manager BIGINT=0; next_supervisor BIGINT=0;
 manager_rate INTEGER=0; supervisor_rate INTEGER=0; recognized TIMESTAMPTZ; revision BIGINT;
BEGIN
 IF NEW.status NOT IN ('succeeded','failed') OR NEW.provider_owner_id <> '00000000-0000-0000-0000-000000000001' THEN RETURN NULL; END IF;
 SELECT * INTO cfg FROM sales_bonus_settings WHERE id=TRUE;
 IF NEW.created_at<cfg.starts_at THEN RETURN NULL; END IF;
 SELECT id,client_manager_id INTO company,manager FROM organizations WHERE id=NEW.owner_id;
 IF company IS NULL THEN RETURN NULL; END IF;
 SELECT * INTO previous FROM generation_profits WHERE task_id=NEW.id;
 base=COALESCE(CASE WHEN NEW.cost->>'actual_source' IN ('free_provider','provider_failed_no_charge') THEN 0::numeric END,
 (NEW.cost->>'tariff_usd')::numeric,(NEW.cost->>'metered_usd')::numeric,
 CASE WHEN NEW.provider_usage->>'currency'='USD' AND jsonb_typeof(NEW.provider_usage->'estimated_cost')='number' THEN GREATEST(0,(NEW.provider_usage->>'estimated_cost')::numeric) END);
 revenue=CEIL(base*(1+COALESCE((NEW.cost->>'markup_percent')::numeric,0)/100)*1000000)::bigint;
 IF COALESCE((NEW.cost->>'billing_required')::boolean,FALSE)
 AND NOT (COALESCE(NEW.cost->>'status','')='confirmed' AND NEW.cost->>'actual_usd' IS NOT NULL AND jsonb_array_length(COALESCE(NEW.cost->'invoice_ids','[]'))>0)
 AND COALESCE(NEW.cost->>'actual_source','') NOT IN ('free_provider','provider_failed_no_charge') THEN
  state='awaiting_billing';
 ELSE
  expense=CEIL(COALESCE(CASE WHEN NEW.cost->>'actual_source' IN ('free_provider','provider_failed_no_charge') THEN 0::numeric END,(NEW.cost->>'actual_usd')::numeric,base)*1000000)::bigint;
  IF revenue IS NOT NULL AND expense IS NOT NULL THEN state='recognized';profit=revenue-expense; END IF;
 END IF;
 IF previous.recognized_at IS NOT NULL THEN
  manager=previous.manager_id;supervisor=previous.supervisor_id;
  manager_rate=previous.manager_percent;supervisor_rate=previous.supervisor_percent;recognized=previous.recognized_at;
 ELSIF state='recognized' THEN
  SELECT sales_supervisor_id INTO supervisor FROM users WHERE id=manager AND is_client_manager;
  manager_rate=cfg.manager_percent;supervisor_rate=cfg.supervisor_percent;recognized=clock_timestamp();
 ELSE manager=NULL; END IF;
 IF state='recognized' THEN
  IF manager IS NOT NULL THEN next_manager=FLOOR(GREATEST(profit,0)::numeric*manager_rate/10000)::bigint; END IF;
  IF supervisor IS NOT NULL THEN next_supervisor=FLOOR(GREATEST(profit,0)::numeric*supervisor_rate/10000)::bigint; END IF;
 END IF;
 IF previous.task_id IS NOT NULL AND (previous.state,previous.revenue_micros,previous.cost_micros,previous.profit_micros,previous.manager_bonus_micros,previous.supervisor_bonus_micros)
 IS NOT DISTINCT FROM (state,revenue,expense,profit,next_manager,next_supervisor) THEN RETURN NULL; END IF;
 revision=COALESCE(previous.revision,0)+1;
 INSERT INTO generation_profits(task_id,organization_id,state,revenue_micros,cost_micros,profit_micros,manager_id,supervisor_id,manager_percent,supervisor_percent,manager_bonus_micros,supervisor_bonus_micros,revision,recognized_at)
 VALUES(NEW.id,company,state,revenue,expense,profit,manager,supervisor,manager_rate,supervisor_rate,next_manager,next_supervisor,revision,recognized)
 ON CONFLICT(task_id) DO UPDATE SET state=EXCLUDED.state,revenue_micros=EXCLUDED.revenue_micros,cost_micros=EXCLUDED.cost_micros,profit_micros=EXCLUDED.profit_micros,
 manager_id=EXCLUDED.manager_id,supervisor_id=EXCLUDED.supervisor_id,manager_percent=EXCLUDED.manager_percent,supervisor_percent=EXCLUDED.supervisor_percent,
 manager_bonus_micros=EXCLUDED.manager_bonus_micros,supervisor_bonus_micros=EXCLUDED.supervisor_bonus_micros,revision=EXCLUDED.revision,recognized_at=EXCLUDED.recognized_at,updated_at=clock_timestamp();
 IF manager IS NOT NULL AND next_manager<>COALESCE(previous.manager_bonus_micros,0) THEN
  INSERT INTO sales_bonus_entries(employee_id,task_id,revision,beneficiary,amount_micros,kind) VALUES(manager,NEW.id,revision,'manager',next_manager-COALESCE(previous.manager_bonus_micros,0),CASE WHEN previous.recognized_at IS NULL THEN 'accrual' ELSE 'correction' END);
 END IF;
 IF supervisor IS NOT NULL AND next_supervisor<>COALESCE(previous.supervisor_bonus_micros,0) THEN
  INSERT INTO sales_bonus_entries(employee_id,task_id,revision,beneficiary,amount_micros,kind) VALUES(supervisor,NEW.id,revision,'supervisor',next_supervisor-COALESCE(previous.supervisor_bonus_micros,0),CASE WHEN previous.recognized_at IS NULL THEN 'accrual' ELSE 'correction' END);
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER generation_profit AFTER INSERT OR UPDATE OF status,cost,provider_usage ON tasks FOR EACH ROW EXECUTE FUNCTION recognize_generation_profit();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to roll back immutable sales bonus history'; END $$;
-- +goose StatementEnd
