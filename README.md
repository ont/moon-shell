# moon-shell

> Warning: this codebase was developed with AI assistance from Codex. Review and test changes carefully before using it in sensitive environments.

`moon-shell` is a Gmail command runner backed by the [`gog`](https://gogcli.sh/) CLI.

It periodically searches unread Gmail messages in inbox and spam, finds messages with a configured subject, extracts a shell command from the email body, downloads the body and attachments into a unique `/tmp` workspace, executes the command there, and replies by email with stdout/stderr summaries and attachments.

Google OAuth and Gmail API access are handled entirely by `gog`; this repository does not implement or store Google OAuth tokens.

## Configuration

The service uses a YAML config file. The default path is `config.yml`.

Create a local config from the example:

```bash
cp config.example.yml config.yml
```

Example:

```yaml
listen_addr: ":8080"

gog:
  binary: gog
  account: your-email@example.com
  subject: moon-shell
  fetch_interval: 5s
  command_timeout: 1m
  workers: 1
  max_results: 50
  temp_pattern: moon-shell-*
  unread_only: true
  search_subject: true
  search_queries:
    - in:inbox
    - in:spam
```

Settings:

- `gog.binary`: path/name of the `gog` executable.
- `gog.account`: Gmail account already authorized in `gog`.
- `gog.subject`: subject substring used to identify command messages.
- `gog.fetch_interval`: polling interval. Defaults to `5s`.
- `gog.command_timeout`: per-command timeout.
- `gog.workers`: number of command workers.
- `gog.max_results`: max messages per search query.
- `gog.temp_pattern`: `/tmp` directory pattern for execution workspaces.
- `gog.unread_only`: append `is:unread` to configured base queries.
- `gog.search_subject`: append `subject:"<configured subject>"` to configured base queries.
- `gog.search_queries`: base Gmail search scopes.

With the example above, the service searches:

```bash
gog gmail messages search 'in:inbox is:unread subject:"moon-shell"' --account your-email@example.com --max 50 --json
gog gmail messages search 'in:spam is:unread subject:"moon-shell"' --account your-email@example.com --max 50 --json
```

The full message is loaded with `gog gmail get`.

## Gog Auth

Install and configure `gog` separately:

```bash
gog auth credentials /path/to/oauth-client.json
gog auth add your-email@example.com --services gmail --force-consent
```

For non-interactive hosts using gog's encrypted file keyring backend, provide the keyring password through the environment expected by `gog`, for example:

```bash
export GOG_KEYRING_PASSWORD='set-this-outside-the-repo'
```

Do not commit OAuth client JSON, service-account JSON, live `config.yml`, token files, or execution databases.

## Run

```bash
go run . --config=config.yml
```

Health endpoints:

- `GET /`
- `GET /healthz`

## Runtime Behavior

For each matching unread message:

1. The service fetches the full message through `gog`.
2. It writes the decoded body to `/tmp/<unique>/body.txt`.
3. It writes attachments directly into `/tmp/<unique>/` using their original filenames, and also copies them under `/tmp/<unique>/attachments/`.
4. It extracts and cleans the shell command from the email body.
5. It executes the command with `bash -lc` inside `/tmp/<unique>/`.
6. It stores execution state in a local SQLite DB so messages are not executed twice.
7. It replies with stdout/stderr in the response body and as `stdout.txt` / `stderr.txt` attachments.
8. It removes the Gmail `UNREAD` label after the response is sent.

The message is marked read even when the command exits with a non-zero code, because non-zero command exits are captured as command results and still produce a response.

## Build

```bash
go build .
```

## Install

The Makefile installs the binary under `/usr/local/bin` and the systemd unit under `/etc/systemd/system`:

```bash
make test
sudo make install
```

The install target also creates `/etc/moon-shell/config.yml` from `config.example.yml` if it does not already exist, and creates an empty `/etc/moon-shell/moon-shell.env` for environment-only settings such as `GOG_KEYRING_PASSWORD`.

Create the service user and state directory if they do not exist:

```bash
sudo useradd --system --home-dir /var/lib/moon-shell --create-home --shell /usr/sbin/nologin moon-shell
sudo chown -R moon-shell:moon-shell /var/lib/moon-shell /etc/moon-shell
```

Configure `/etc/moon-shell/config.yml` and `/etc/moon-shell/moon-shell.env`, then enable the service:

```bash
sudo systemctl enable --now moon-shell.service
sudo systemctl status moon-shell.service
```

Override install locations if needed:

```bash
sudo make install PREFIX=/usr/local SYSCONFDIR=/etc
```
