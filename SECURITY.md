# Security policy

This is a university distributed-systems assignment. Part I provides a dummy
REST/gRPC game API for development and demonstration.

## Reporting a vulnerability

If GitHub offers **Report a vulnerability** under this repository's Security
tab, use that private reporting option.

Otherwise, open an issue asking for a private reporting channel without
including vulnerability details. Share the details once a private channel has
been agreed. Do not post credentials, student IDs, private data, or working
exploit instructions in a public issue.

A useful report includes the affected commit, a short description of the impact,
and the steps needed to reproduce the problem in a local test environment.

## Security scope

- Clients are assumed to be trusted and to use their own player IDs.
- The application has no authentication or authorization layer.
- The supplied development configuration uses HTTP and plaintext gRPC.
- Part I returns fixed dummy data and stores no application state.

Use the supplied configuration on a trusted development machine or network.
Deployments exposed to untrusted users need authentication, authorization,
transport security, and a review of the game-state implementation.

## Supported versions

The current course branch is maintained as coursework. There is no supported
production release or guaranteed security-response schedule.
