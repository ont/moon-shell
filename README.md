# moon-shell

Simple Go service that:

- exposes Echo HTTP endpoints on `/` and `/healthz`
- authenticates to Gmail with OAuth2
- periodically fetches drafts for subject `moon-shell`
- optionally registers Gmail watch and consumes Pub/Sub notifications
- queues matching messages and executes them with a worker pool

## Configuration

The service supports:

- YAML config file, default path: `config.yml`
- one CLI flag: `--config`

Precedence is:

1. built-in defaults
2. YAML config
3. `--config` only selects which YAML file to load

The easiest way to start is to copy [config.example.yml](/home/ont/petproj/moon-shell/config.example.yml) to `config.yml` and fill in the Gmail OAuth values.

Example:

```bash
cp config.example.yml config.yml
```

Minimal `config.yml`:

```yaml
listen_addr: ":8080"

gmail:
  user: me
  subject: moon-shell
  fetch_interval: 1m
  command_timeout: 1m
  workers: 2
  max_results: 25
  client_id: your-oauth-client-id.apps.googleusercontent.com
  client_secret: your-oauth-client-secret
  watch:
    enabled: false
    project_id: your-google-cloud-project-id
    topic_name: projects/your-google-cloud-project-id/topics/gmail-watch
    subscription_id: gmail-watch-sub
    credentials_file: /full/path/to/service-account-key.json
    renew_before: 1h
```

The app initializes a config struct with defaults and then unmarshals YAML on top of it. That means fields omitted in YAML keep their default values, while fields present in YAML replace them.

Execution settings:

- `gmail.command_timeout`: per-command execution timeout
- `gmail.workers`: number of background workers consuming the internal queue
- fetch/watch delivery and command execution are decoupled; matching messages are enqueued first and workers process them in parallel

CLI does not override runtime settings. It only selects the config file path:

```bash
go run . --config=config.yml
```

## Gmail OAuth Setup

This service expects three Gmail OAuth values:

- `gmail.client_id`
- `gmail.client_secret`

The app uses the Gmail readonly scope and refreshes short-lived access tokens automatically from a locally stored refresh token file.
The refresh token is not stored in `config.yml`.

### 1. Create a Google Cloud project and enable the Gmail API

- In Google Cloud, enable the Gmail API for the project you will use.
- Configure the OAuth consent screen for that project.

### 2. Create an OAuth client

For local or operator-driven use, a Desktop app OAuth client is the simplest option.

- Create an OAuth 2.0 client ID.
- Choose `Desktop app`.
- Download the generated client JSON.

From that JSON:

- `client_id` maps to `gmail.client_id`
- `client_secret` maps to `gmail.client_secret`

### 3. Obtain and store the refresh token

Your refresh token must be issued with offline access for the Gmail scope:

- scope: `https://www.googleapis.com/auth/gmail.readonly`
- offline access is required so the service can refresh access tokens without interactive login

Run the built-in auth helper:

```bash
go run . --config=config.yml auth init
```

What it does:

- starts a temporary local HTTP callback server on `127.0.0.1`
- prints a Google authorization URL
- waits for you to approve access in the browser
- exchanges the authorization code for OAuth tokens
- stores the resulting token in `config.yml.token.json`

At runtime, the service reads the refresh token from that token file.

Notes:

- Google may only return a refresh token on the first consent for a given client/scope/user combination unless consent is forced again.
- If you change scopes, you may need to repeat the authorization flow and obtain a new refresh token.
- Keep `client_secret` and `config.yml.token.json` out of version control.

## Gmail Watch Setup

Watch mode is optional. If `gmail.watch.enabled` is `true`, you must configure Pub/Sub and local credentials for the Pub/Sub client.

Required watch fields:

- `gmail.watch.project_id`
- `gmail.watch.topic_name`
- `gmail.watch.subscription_id`
- `gmail.watch.renew_before`

Optional watch field:

- `gmail.watch.credentials_file`

Operational requirements:

- The Pub/Sub topic must already exist.
- Gmail must be allowed to publish to that topic.
- The process also needs Google Cloud credentials for Pub/Sub access, typically via Application Default Credentials.

For local development, the common path is:

```bash
gcloud auth application-default login
```

For service-account based deployment, you can either set:

```bash
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
```

or put the same path into YAML:

```yaml
gmail:
  watch:
    credentials_file: /path/to/service-account.json
```

## Run Locally

With YAML config:

```bash
go run . --config=config.yml auth init
go run . --config=config.yml
```

## Gog-Backed Version

This repository also contains a second implementation that delegates all Google/Gmail operations to the `gog` CLI instead of using Google SDK OAuth clients directly.

Prepare gog auth separately:

```bash
gog auth credentials /path/to/client_secret.json
gog auth add your-email@example.com --services gmail --force-consent
```

Create a config:

```bash
cp gog-config.example.yml gog-config.yml
```

Minimal `gog-config.yml`:

```yaml
listen_addr: ":8081"

gog:
  binary: gog
  account: your-email@example.com
  subject: moon-shell
  fetch_interval: 5s
  command_timeout: 1m
  workers: 1
  max_results: 50
  temp_pattern: moon-shell-gog-*
  unread_only: true
  search_subject: true
  search_queries:
    - in:inbox
    - in:spam
```

Run it:

```bash
go run ./cmd/moon-shell-gog --config=gog-config.yml
```

The service periodically runs message-level gog searches equivalent to:

```bash
gog gmail messages search 'in:inbox is:unread subject:"moon-shell"' --account your-email@example.com --max 50 --json
gog gmail messages search 'in:spam is:unread subject:"moon-shell"' --account your-email@example.com --max 50 --json
```

For matching subjects, it loads each message with `gog gmail get`, writes the body and attachments to a unique `/tmp/moon-shell-gog-*` directory, executes the extracted command inside that directory, and replies with `gog gmail send`. `stdout.txt` and `stderr.txt` are attached to the response and also summarized in the response body.

`gog.search_queries` are base Gmail search scopes. By default, the service appends `is:unread` and `subject:"<configured subject>"` to every base query. Set `gog.unread_only: false` or `gog.search_subject: false` if you need broader polling.

## Build Image

```bash
docker build -t moon-shell .
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yml:/app/config.yml:ro" \
  -v "$PWD/config.yml.token.json:/app/config.yml.token.json:ro" \
  moon-shell \
  --config=/app/config.yml
```

If Gmail watch is enabled in Docker, you must also provide Google Cloud credentials for Pub/Sub access.

## References

- Gmail API overview and auth: https://developers.google.com/workspace/gmail/api
- OAuth 2.0 offline access and refresh tokens: https://developers.google.com/identity/protocols/oauth2/web-server
- OAuth consent screen: https://developers.google.com/workspace/guides/configure-oauth-consent
- Application Default Credentials: https://cloud.google.com/docs/authentication/provide-credentials-adc
- Gmail push notifications / watch: https://developers.google.com/workspace/gmail/api/guides/push
