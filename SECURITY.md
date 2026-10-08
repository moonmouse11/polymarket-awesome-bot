# Security Policy

## Supported versions

The project has no releases yet. Only the latest code on the `main` branch is supported.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report privately through GitHub: open the repository's **Security** tab and click **Report a vulnerability** ([direct link](https://github.com/moonmouse11/polymarket-awesome-bot/security/advisories/new)).

Please include:

- what the problem is and where (file, command, configuration);
- steps to reproduce;
- the impact you expect.

This is a personal project maintained on a best-effort basis. You will get a reply as soon as possible; once a fix is ready, the advisory is published with credit to you unless you prefer otherwise.

## Scope

Especially relevant:

- leaks of secrets (`.env`, MongoDB passwords, API tokens) through logs, images or the repository;
- ways to reach MongoDB or the bot's HTTP port from outside the host;
- privilege escalation of the bot's MongoDB user or the container user.
