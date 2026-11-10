# CLI browser sign-in

`orbits login` signs the CLI in through the Orbits web app, the way `gh auth login` does: the person approves the CLI
in their browser, the web app sends a one-time code to a loopback server the CLI runs, and the CLI trades that code,
with a PKCE verifier, for a new API token. The token itself never appears in a URL.

This describes what the CLI and the web app send each other. The CLI side is
[`internal/auth/browser.go`](../internal/auth/browser.go). The web app is `https://orbits.africa`; `ORBITS_WEB_URL`
points the CLI at another (staging).

## Overview

```
CLI                                   Browser / web app                      Orbits API
 |  listen on 127.0.0.1:<port>          |                                       |
 |  verifier = random, challenge = S256(verifier), state = random               |
 |--- open GET /cli/authorize?port&state&challenge&host -->|                    |
 |                                      |  request -> cookie, sign in if needed |
 |                                      |  pick team, access, expiry            |
 |                                      |  on Approve: create a token --------->|
 |                                      |  seal {token, challenge} in the code  |
 |<-- browser navigates to http://127.0.0.1:<port>/callback?state&code         |
 |--- POST /api/cli/token {code, code_verifier} -------->|                      |
 |<-- 200 {token, token_id, team, access, ...} ----------|                      |
 |--- GET /v1/me (Bearer token) ----------------------------------------------->|
 |  callback tab answered "You're signed in"                                    |
```

## 1. Probe: `GET /cli/authorize` (no query)

Before opening the browser, the CLI requests `GET <web>/cli/authorize` with no query, without following redirects.
The web app answers `200` (a "Start from your terminal" page). **Only a `404` matters**: it means this web app has no
CLI sign-in, and the CLI stops at once, pointing to `orbits login --with-token`. Any other answer, or none, lets the
sign-in go ahead.

## 2. Authorize: `GET /cli/authorize`

The CLI opens, in the person's browser:

```
https://orbits.africa/cli/authorize?port=53682&state=<state>&challenge=<challenge>&host=<host>
```

| Parameter | CLI sends | Web app accepts |
| --- | --- | --- |
| `port` | The loopback port the OS gave. | An integer, `1024`–`65535`. |
| `state` | 32 characters, base64url without padding (24 random bytes). | 16–128 base64url characters. Echoed back unchanged. |
| `challenge` | `BASE64URL-NOPAD(SHA-256(code_verifier))`. Always S256. | Exactly 43 base64url characters. |
| `host` | The machine's name reduced to `[A-Za-z0-9 .@_-]`, trimmed, at most 60 characters. May be empty. | At most 60 of `[\w .@-]`. Only names the token: `CLI on <host>`. |

The web app keeps the request in a short-lived `httpOnly` cookie, signs the person in if needed, and shows the machine,
a team picker (teams where they are an owner or admin), the access (full, or read only) and the expiry (30, 90 or 365
days, or never).

On **Approve** it creates an API token in the chosen team, seals `{token, challenge}` into a two-minute, single-use
code with authenticated encryption, and sends the browser to:

```
http://127.0.0.1:<port>/callback?state=<state>&code=<code>
```

On **Cancel**: `http://127.0.0.1:<port>/callback?state=<state>&error=access_denied`. Nothing is created.

The callback is always `http://127.0.0.1`, never `localhost`.

## 3. Callback: `GET http://127.0.0.1:<port>/callback` (served by the CLI)

| Query | CLI does |
| --- | --- |
| `state` missing or different | `400`, and goes on waiting for its own. |
| `state` and `error=<anything>` | Shows "Sign-in cancelled" and stops: "the sign-in was cancelled in the browser". |
| `state` and `code` | Trades the code (step 4), checks the token with `GET /v1/me`, then answers this request with "You're signed in" or "Sign-in failed". The tab waits up to 45 seconds for that answer. |

The CLI waits five minutes for the callback, then stops and suggests `--with-token`.

## 4. Exchange: `POST /api/cli/token`

```http
POST /api/cli/token HTTP/1.1
Host: orbits.africa
Content-Type: application/json

{"code":"<code>","code_verifier":"<verifier>"}
```

`code_verifier` is 43 characters of base64url without padding (32 random bytes). The code and the verifier are the
authentication: no cookie, no session.

`200`, `Cache-Control: no-store`:

```json
{
  "token": "orb_live_…",
  "token_id": "tok_…",
  "token_name": "CLI on dev-box",
  "team": { "id": "team_…", "slug": "my-team", "name": "My team" },
  "access": "full",
  "expires_at": "2027-02-08T10:00:00Z",
  "api_url": "https://api.theorbits.app"
}
```

| Field | Notes |
| --- | --- |
| `token` | The secret, `orb_live_…`. The CLI refuses an answer without it. |
| `token_id` | Named if the token then fails the check, so it can be revoked. |
| `team` | The team the token acts in; it becomes the profile's team. |
| `access` | `full` or `read`. A read token can only make `GET` calls. |
| `expires_at` | RFC 3339, or `null` for a token that never expires. |
| `api_url` | The API the web app talks to, without `/v1`. The CLI uses it (with `/v1`) for this profile unless `ORBITS_API_URL` is set, and only when it is `https` (or a loopback address). |

Every failure of the code (wrong verifier, expired, used, forged) is one `400` problem document with
`code: INVALID_GRANT`, so they can't be told apart. Too many attempts from one address is `429` with
`code: RATE_LIMITED` and `Retry-After`. The CLI says so and stops; it does not retry a code.

## 5. Check and store

The CLI calls `GET /v1/me` with the token. Only if that works does it store the token (keychain, else
`credentials.json` with mode `0600`), save the profile (team, token id and name, access, expiry, API) and tell the
browser tab it worked. If the check fails, nothing is stored and the person is told the token id to revoke under
Team › Settings › API tokens.

## Without a browser

`orbits login --with-token` reads a token made under Team › Settings › API tokens from stdin (hidden when typed), checks
it with `GET /v1/me`, and stores it the same way; the team is the one `/me` shows the token. `ORBITS_TOKEN` skips
storing anything.
