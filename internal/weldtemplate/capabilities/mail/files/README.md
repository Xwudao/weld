# internal/mailsender

The SMTP mail sender of this project, built from the typed configuration. It is
opt-in and explicit: `weld add mail` installs the sender, it connects mail to
nothing. Nothing in the generated application imports this package, so building,
testing and serving send no mail and need no SMTP server.

## Sending is one call

```go
sender, err := mailsender.New(cfg) // validates the mail section; no connection
if err != nil {
    return err
}
err = sender.Send(ctx, mailsender.Message{
    To:      []string{"user@example.com"},
    Subject: "Hello",
    Text:    "plain text body",
    HTML:    "<p>HTML body</p>",
})
```

`New` validates the `mail` section and stores the settings. It sends nothing and
opens no connection. Every `Send` builds a go-mail client, dials the configured
server, applies the configured TLS policy, authenticates when credentials are
set, submits the message and quits. There is no connection pool, no background
goroutine, no queue and no outbox; `ctx` and the configured `timeout_seconds`
bound each call. The SMTP session and the MIME message are
`github.com/wneessen/go-mail`'s job, so this package does not hand-write the
protocol or the message format.

When both `Text` and `HTML` are set the message is `multipart/alternative` with a
`text/plain` part followed by a `text/html` part, so a mail client shows the
first format it understands. With a single body it is a single part of that
type. `From` is optional and falls back to the configured sender. `Bcc`
recipients receive the message but never appear in its headers.

Every value that becomes an SMTP header — the subject, the sender header and
each recipient — is rejected if it contains a carriage return or a line feed, so
a crafted value cannot inject a header. Envelope addresses are parsed and
reduced to their bare form.

## Explicit TLS policy

`mail.tls` is a closed set and there is no opportunistic upgrade:

| value | behavior |
| --- | --- |
| `none` | an unencrypted connection. Credentials are refused with this policy, so they can never cross the wire in the clear |
| `starttls` | a plain connection, then a required `STARTTLS` upgrade before any credential is sent. A server that does not advertise `STARTTLS` fails the send |
| `implicit` | TLS from the first byte (SMTPS), before the SMTP greeting |

Production TLS requires TLS 1.2 or newer and verifies the server certificate:
the sender never sets `InsecureSkipVerify`, so a self-signed server fails the
send instead of being trusted silently. A server-supplied error reply can echo
the base64 credential it received during `AUTH`, so `Send` never returns the
library's error text; it reports a fixed failure and preserves an expired or
canceled context.

## Configuration

The configuration lives in `internal/config` (`config.Mail`) and is read from
`config.yml` (`mail:` section) and overridden by the environment (`MAIL_HOST`,
`MAIL_PORT`, `MAIL_USERNAME`, `MAIL_PASSWORD`, `MAIL_FROM`, `MAIL_TLS`,
`MAIL_TIMEOUT_SECONDS`). The username and password are `config.Secret`, which
redacts itself in `fmt`, `log/slog`, JSON and YAML, so a credential cannot reach
a log record by accident. The port defaults to `587` and the timeout to `10`
seconds; the credentials never have a default. The `mail` section is validated
only when `New` builds a sender, so a project that installs mail but never sends
needs no mail setting.

`mailsender.New` takes a `mailsender.Provider`, the one-method interface
`MailSettings() config.Mail`. `*config.Config` satisfies it today, so the sender
reads YAML and the environment without knowing either. A future database-backed
site configuration implements that one method and replaces the YAML source
without changing the sender, this package or its tests.

## With Loom

`weld add mail` writes
`internal/di/mail_provider.go`, a stable seam that declares `NewMailSender` as an
available binding. The generated graph declares it but nothing depends on
`*mailsender.Sender`, so Loom prunes it: an ordinary serve constructs no sender
and needs no mail setting.

To use the sender, make a provider the graph already consumes depend on
`*mailsender.Sender`. `mail_provider.go` is written once and never regenerated,
so those edits survive every later `weld add`.

Because `NewMailSender` never dials and never sends, building the graph, running
tests and starting the process still send no mail; the first `Send` is the first
connection.

## Tests

The tests are focused: they prove the constructor is idle, that message
validation rejects a malformed envelope before any connection, and that one send
reaches a minimal in-process SMTP server on loopback. The SMTP session and the
MIME message are go-mail's job, so there is no protocol implementation to test
here. No external SMTP server is needed:

```sh
go test ./internal/mailsender/...
```
