# Create, poll and download a mock image

Start a local installation and create your owner account with the
[Quick Start](../README.md#quick-start), or use an existing Gateway installation.

Create a key in **API keys** after signing in, then set `GATEWAY_API_KEY` to its
secret for the examples below. Use your console's HTTPS origin on a server.
`CLIENT_API_KEY` is a separate service credential, not a browser password.

```sh
export GATEWAY_API_KEY='YOUR_GATEWAY_API_KEY'
```

The `mock` adapter returns fixed test media after a short delay. It does not
render the prompt; the image fixture is an SVG and no paid provider request is
made. Real provider models need your own credentials and can incur charges.
This example uses only `mock`.

The following shell example also needs `curl` and Python 3. It polls up to 60
times, then downloads the completed image with the same API key. Keep the
Gateway base URL free of a trailing slash.

```sh
(
set -eu
GATEWAY_URL=http://localhost:8088
generation=$(curl --fail --silent --show-error -X POST "$GATEWAY_URL/v1/images/generations" \
  -H "Authorization: Bearer $GATEWAY_API_KEY" \
  -H "Idempotency-Key: mock-image-$(python3 -c 'import uuid; print(uuid.uuid4())')" \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"A cinematic lighthouse at dawn","model":"mock/mock-image-v1"}')
status_url=$(printf '%s' "$generation" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status_url"])')
status=queued
attempt=0
while [ "$attempt" -lt 60 ]; do
  generation=$(curl --fail --silent --show-error \
    -H "Authorization: Bearer $GATEWAY_API_KEY" "$GATEWAY_URL$status_url") || break
  status=$(printf '%s' "$generation" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])')
  case "$status" in completed|failed) break ;; esac
  attempt=$((attempt + 1))
  sleep 2
done
if [ "$status" = completed ]; then
  content_url=$(printf '%s' "$generation" | python3 -c 'import json,sys; print(json.load(sys.stdin)["content_url"])')
  curl --fail --location --show-error \
    -H "Authorization: Bearer $GATEWAY_API_KEY" \
    "$GATEWAY_URL$content_url" --output mock-image.svg
else
  printf 'Generation did not complete (status: %s). Inspect status_url before retrying.\n' "$status" >&2
  exit 1
fi
)
```

Poll the returned `status_url` until the generation reaches `completed` or `failed`. The
complete HTTP contract is in the [OpenAPI specification](openapi.yaml). Authenticated
task responses include retained `prompt` and `options` only when the current
viewer has content access. Webhook `data` uses the public generation object;
request content follows the same retention policy.

For endpoint details and migration guidance, see the
[asynchronous API guide](openai-api.md). Your running Gateway serves its API
reference at `/docs`, with contracts at `/openapi.yaml` and `/openapi.json`.
