# Security policy

This is a university distributed-systems assignment. Part II is a stateful
REST/gRPC game service intended for a trusted development environment.

## Reporting a vulnerability

If GitHub offers **Report a vulnerability** in this repository's Security tab,
use that private reporting option. Otherwise, open an issue asking for a private
reporting channel without including vulnerability details. Share details after
a private channel has been agreed.

A useful report includes the affected commit, impact, and local reproduction
steps. Do not post credentials, private data, or working exploits publicly.

## Security scope

- Clients are trusted to use their own player IDs; there is no authentication.
- The development configuration uses HTTP and plaintext gRPC.
- Public game responses omit hands and deck order. Hand reads identify a player.
- Request bodies are validated against OpenAPI and limited to 1 MiB.
- Game capacity and batch/name limits bound logical application inputs.
- State and snapshots include private hands; keep snapshot files private when
  they contain data from an actual session.
- State is in memory; there is no automatic persistence or request deduplication.

An externally deployed version would need authentication, authorization,
transport security, resource controls and a separate deployment review.

## Supported versions

The current course branch is maintained as coursework. There is no supported
production release or guaranteed security-response schedule.
