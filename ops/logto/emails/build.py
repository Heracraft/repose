#!/usr/bin/env python3
"""Builds the verification-code emails Logto sends from accounts.herakraft.co.

The tenant is shared by repose and the recruiting app (Job Alerts), so no
template names a product: the name comes from {{application.name}}, which
Logto fills with the name of the application the person is signing in to
(docs/ops/logto.md). Generic and OrganizationInvitation carry no application
in Logto's payload, so those two say "Herakraft".

Logto substitutes {{var}} with a regex and nothing else: no conditionals, no
loops, and a variable whose root is missing from the payload is left in the
email as typed. Only {{code}}, {{link}}, {{application.name}} and
{{application.branding.logoUrl}} are used, each only on the usage types
whose payload carries it.

Run: python3 ops/logto/emails/build.py
Writes templates.json (the connector's `templates` array) and preview/*.html
with sample values filled in.
"""

import html
import json
import pathlib

HERE = pathlib.Path(__file__).parent

APP = "{{application.name}}"
HOUSE = "Herakraft"

# The house palette (internal/api/notify/templates/layout.html).
INK = "#181817"
BODY = "#3e3e3b"
MUTED = "#767671"
RULE = "#e6e6e3"
SUNKEN = "#f4f4f2"
SERIF = "'Noto Serif',Georgia,'Times New Roman',serif"
SANS = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"
MONO = "ui-monospace,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace"

EXPIRY = "Expires in 10 minutes."
# Inbox lists show the preheader and then keep reading into the body; this
# run of invisible characters fills the rest of the preview line.
PAD = "&#847;&zwnj;&nbsp;" * 90
IGNORE = "If you didn't request this code, ignore this email."

# usage type -> (brand, subject, heading, lead)
KINDS = {
    "SignIn": (APP, "{{code}} is your " + APP + " sign-in code",
               "Your sign-in code", "Enter this code to sign in to " + APP + "."),
    "Register": (APP, "{{code}} is your " + APP + " verification code",
                 "Confirm your email", "Enter this code to create your " + APP + " account."),
    "ForgotPassword": (APP, "{{code}} is your " + APP + " password reset code",
                       "Reset your password", "Enter this code to reset your " + APP + " password."),
    "UserPermissionValidation": (APP, "{{code}} is your " + APP + " verification code",
                                 "Confirm the change", "Enter this code to confirm the change to your " + APP + " account."),
    "BindNewIdentifier": (APP, "{{code}} is your " + APP + " verification code",
                          "Confirm your email", "Enter this code to add this email to your " + APP + " account."),
    "MfaVerification": (APP, "{{code}} is your " + APP + " two-step code",
                        "Your two-step code", "Enter this code to finish signing in to " + APP + "."),
    "BindMfa": (APP, "{{code}} is your " + APP + " verification code",
                "Turn on two-step sign-in", "Enter this code to turn on two-step sign-in for " + APP + "."),
    "Generic": (HOUSE, "{{code}} is your verification code",
                "Your verification code", "Enter this code to continue."),
}


def page(brand, subject, preheader, heading, lead, footer, middle, mark=False):
    # Logto fills subject variables before the body's, but <title> is only
    # read by a few clients; the preheader is what the inbox list shows.
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>{subject}</title>
</head>
<body style="margin:0;padding:0;background:#ffffff;-webkit-text-size-adjust:100%;">
<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;font-size:1px;line-height:1px;color:#ffffff;">{preheader}{PAD}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#ffffff;">
<tr><td align="center" style="padding:40px 20px 48px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:480px;">
<tr><td style="padding:0 0 36px;">{masthead(brand, mark)}</td></tr>
<tr><td style="padding:0 0 10px;font-family:{SERIF};font-size:24px;line-height:32px;font-weight:600;color:{INK};">{heading}</td></tr>
<tr><td style="padding:0 0 24px;font-family:{SANS};font-size:15px;line-height:24px;color:{BODY};">{lead}</td></tr>
{middle}
<tr><td style="padding:32px 0 0;"><div style="border-top:1px solid {RULE};font-size:0;line-height:0;">&nbsp;</div></td></tr>
<tr><td style="padding:16px 0 0;font-family:{SANS};font-size:13px;line-height:20px;color:{MUTED};">{footer}</td></tr>
</table>
</td></tr>
</table>
</body>
</html>
"""


# Usage types that only happen inside a sign-in, where Logto always knows
# the application from the interaction cookie. Account changes can come
# from Logto's built-in account center, which has no logo, so those emails
# keep the name alone rather than risk an empty <img>.
WITH_MARK = {"SignIn", "Register", "ForgotPassword", "MfaVerification"}


def masthead(brand, mark):
    # The mark is each application's sign-in logo, a PNG over https since
    # Gmail shows neither SVG nor data: images (ops/logto/README.md).
    # alt is empty: the name beside it already says it.
    name = (f'<td style="font-family:{SERIF};font-size:18px;line-height:24px;font-weight:600;'
            f'letter-spacing:-0.01em;color:{INK};">{brand}</td>')
    if not mark:
        return f'<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>{name}</tr></table>'
    img = ('<td width="24" style="width:24px;padding:0 10px 0 0;"><img src="{{application.branding.logoUrl}}" '
           'width="24" height="24" alt="" style="display:block;border:0;width:24px;height:24px;"></td>')
    return f'<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>{img}{name}</tr></table>'


def code_block():
    # One full-width surface with the code centred in it, nothing boxed around it. Monospace so 0
    # and O, 1 and l can't be confused; the spacing makes it readable aloud.
    return f"""<tr><td style="padding:0 0 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr>
<td align="center" style="background:{SUNKEN};border:1px solid {RULE};border-radius:3px;padding:18px 16px 18px 22px;text-align:center;font-family:{MONO};font-size:32px;line-height:40px;font-weight:600;letter-spacing:0.2em;color:{INK};">{{{{code}}}}</td>
</tr></table>
</td></tr>
<tr><td align="center" style="font-family:{SANS};font-size:13px;line-height:20px;color:{MUTED};text-align:center;">{EXPIRY}</td></tr>"""


def invitation():
    subject = "You're invited to join an organization"
    middle = f"""<tr><td style="padding:0 0 12px;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
<td style="background:{INK};border-radius:2px;"><a href="{{{{link}}}}" style="display:inline-block;padding:10px 20px;font-family:{SANS};font-size:14px;line-height:20px;font-weight:600;color:#ffffff;text-decoration:none;">Accept the invitation</a></td>
</tr></table>
</td></tr>"""
    return subject, page(
        HOUSE,
        subject,
        "Open the link to accept.",
        "You're invited",
        "You've been invited to join an organization.",
        "If you weren't expecting this, ignore this email.",
        middle,
    )


def main():
    templates = []
    for usage, (brand, subject, heading, lead) in KINDS.items():
        pre = EXPIRY
        content = page(brand, html.escape(subject, quote=False), pre, heading,
                       html.escape(lead, quote=False), html.escape(IGNORE, quote=False), code_block(),
                       mark=usage in WITH_MARK)
        templates.append({"usageType": usage, "contentType": "text/html", "subject": subject,
                          "content": content})
    subject, content = invitation()
    templates.append({"usageType": "OrganizationInvitation", "contentType": "text/html",
                      "subject": subject, "content": content})

    (HERE / "templates.json").write_text(json.dumps(templates, indent=2) + "\n")

    preview = HERE / "preview"
    preview.mkdir(exist_ok=True)
    for t in templates:
        filled = t["content"].replace("{{code}}", "665885").replace("{{application.name}}", "repose")
        filled = filled.replace("{{link}}", "https://example.com/invite")
        filled = filled.replace("{{application.branding.logoUrl}}", "https://repose.run/favicon.png")
        (preview / f"{t['usageType']}.html").write_text(filled)
    print(f"{len(templates)} templates")


if __name__ == "__main__":
    main()
