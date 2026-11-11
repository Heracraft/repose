# Logto at accounts.herakraft.co

repose signs people in through the owner's Logto (DECISIONS I-84). The same
tenant serves the recruiting app, Job Alerts, so every setting below either
names no product or belongs to one application (I-340). Admin console:
`https://logto-admin-lehvjtiqbgmf5dsksyjizxxw.unwrap.link/`.

## Applications

| App ID | Type | Name | Used by |
| --- | --- | --- | --- |
| `osfcu4s5rg0p6tn91lpb8` | SPA | `repose` | dashboard (`PUBLIC_LOGTO_APP_ID`) |
| `jccig5bb3i4d78bq4farv` | Native, device flow | `repose` | CLI (`defaultLogtoClientID`, I-99) |
| `ev18qy8l3y8wslzoyxmjt` | Machine-to-machine | `repose api` | api's Management API calls (I-87) |
| `wc2np1n3r9z4acp2wutev` | Traditional | `repose feedback` | Fider board at `repose.fider.io` (I-415, `ops/fider/README.md`) |
| `kcl4gnzsa68ttme40xy9p` | Traditional | `Job Alerts` | recruiting app |

The name is what the verification emails say (`{{application.name}}`), so
renaming an app renames it in every email it causes.

Each repose app has an application sign-in experience (Applications, the
app, Branding): display name `repose`, logo
`https://repose.run/favicon.png` (the mark), terms
`https://repose.run/terms`, privacy
`https://repose.run/privacy`. Job Alerts has display name `Job
Alerts` and logo `https://recruiting.herakraft.co/apple-touch-icon.png`.
The logo is also the mark in the sign-in emails
(`{{application.branding.logoUrl}}`), so it has to be a PNG or JPEG over
https: Gmail shows neither SVG nor `data:` images. `logo-mark.svg` is the
drawing, kept for a larger PNG render.

## Sign-in experience (tenant-wide)

- Sign-in: email with a verification code, or GitHub. Sign-in and
  register on one page.
- Social: automatic account linking on.
- Custom CSS: `ops/logto/sign-in.css`, pasted whole.
- Unknown-session fallback: `https://repose.run`.
- MFA: passkey and TOTP offered, never prompted.

## Emails

Connector: SMTP (`8y9x2byc8npj`) through Resend, from
`login@accounts.herakraft.co`, replies to `support@herakraft.co`.

The nine templates come from `ops/logto/emails/build.py`:

```
python3 ops/logto/emails/build.py   # writes templates.json and preview/*.html
```

`templates.json` is the connector's `templates` array. Each entry also gets
`"sendFrom": "{{application.name}} <login@accounts.herakraft.co>"` (Generic
and OrganizationInvitation: `Herakraft <login@...>`) when applied; the
console's form doesn't show `sendFrom`, so apply through the Management API
(`PATCH /api/connectors/8y9x2byc8npj` with the whole `config`, the SMTP
password included, read back from a `GET` of the same connector). Open
`preview/SignIn.html` to look at one before applying.

Only `{{code}}`, `{{link}}`, `{{application.name}}` and, in the four
sign-in usage types, `{{application.branding.logoUrl}}` are used: Logto
replaces variables with a regex, has no conditionals, and leaves a variable
whose root is missing in the email as typed.
