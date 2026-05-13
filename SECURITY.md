# Security Policy

## Reporting a Vulnerability

If you find a security issue in prompt-cleaner, **please do not open a
public GitHub issue**. Instead, report it privately via one of the
following channels:

- GitHub Security Advisories — use the **"Report a vulnerability"**
  button under the repository's *Security* tab.
- Email: `anthony.brco@gmail.com` with the subject line
  `[prompt-cleaner security]`.

Please include:

- A description of the issue.
- Affected versions / commits.
- Steps to reproduce.
- Impact assessment (what an attacker can do).

You should expect an acknowledgement within **5 business days**.
A fix or mitigation timeline will be agreed on a case-by-case basis.

## Threat Model

prompt-cleaner is a **localhost development proxy**. It is deliberately
unauthenticated and binds to `127.0.0.1` by default. It is **not**
intended to be exposed on a network, a container behind a public
ingress, or any multi-tenant environment.

In particular, the proxy:

- Captures `x-api-key` and `Authorization` headers verbatim into the
  in-memory capture log. Anything that can reach the UI port can read
  every request that has flowed through the proxy.
- Lets any reachable client toggle intercept, replay arbitrary requests
  to the configured upstream, and rewrite request/response bodies via
  regex rules.
- Has no TLS termination of its own (the proxy listens plain HTTP).

If you bind `-proxy-addr` or `-ui-addr` to anything other than
`127.0.0.1`, you are responsible for placing access control in front of
it (firewall, SSH tunnel, reverse proxy with auth, etc.).

## Supported Versions

Only the latest tagged release is supported. Older versions receive
fixes only at the maintainer's discretion.
