# Setup

This document describes how to set up nova-tools dependencies and verify them.

## dep-jev-b.w3~15.g3: Jev (nova-decide's backend)

**Dependency:** TypeSafe Jev endpoint for decision-making  
**Who needs it:** nova-decide and any tool that calls internal/decide  
**How nova-up provides it:** via `internal/up/jev.go` which documents setup requirements  
**What the doctor checks:** JEV_API_KEY present in the environment by name (never printed) and the endpoint answering a no-cost health call

### Dependency details

Jev is the TypeSafe Jev backend that nova-decide uses for decision-making. The key is stored in the secrets store by name (JEV_API_KEY) and is never printed anywhere. The endpoint can be health-checked through a fake-able client.

### Setup

1. Set `JEV_API_KEY` environment variable to your TypeSafe Jev API key
2. (Optional) Set `TYPESAFE_API_KEY` as a fallback if JEV_API_KEY is not available
3. For local testing, the check uses a no-op health client that doesn't require network access

### What is lost without it

When JEV_API_KEY is unset, decision-making via nova-decide is skipped. This is optional: a sprint can run without decisions, but the doctor check will warn that Jev is unavailable.

---

## Other dependencies

- See individual card sections for additional dependencies

