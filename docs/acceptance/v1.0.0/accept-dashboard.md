Verdict: HOLD

This acceptance check could not be run because the nova-sprint dashboard server is not running. The dashboard endpoint at http://127.0.0.1:7390/api/sprint returns "Connection refused".

Build measured: nova-sprint v1.2.0-dev.8388d032 linux/amd64 go1.27.1

Commands attempted:
- nova-sprint --version
- curl -s http://127.0.0.1:7390/api/sprint

The measurement window could not be executed. Per the card rules, a card that cannot be measured is written as HOLD with the precise blocker.

---
By: Freddy (inception/mercury-2.5 via opencode)
