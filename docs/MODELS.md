# Model routes — every route a bench can run

> Glenn, 2026-09-16: "lock all these available models in somewhere so we do not
> lose the knowledge."

This is the registry of every model route a bench can run: the provider, the key
location by path, the model ids, the cost class and the probe verb. A route is
real only when it is here. `nova-swarm native` and `nova-swarm batch` both point
here for the routes (`routes: see docs/MODELS.md`), so a route is never forgotten
again.

## The routes

| route | provider block | key | cost class | notes |
| --- | --- | --- | --- | --- |
| `deepseek/deepseek-flash` | `deepseek` | `apiKey {env:DEEPSEEK_API_KEY}` — `~/.config/deepseek/env`, bare key, mode 0600 | metered | direct `api.deepseek.com` |
| `deepseek/deepseek-v4-pro` | `deepseek` | `apiKey {env:DEEPSEEK_API_KEY}` — `~/.config/deepseek/env`, bare key, mode 0600 | metered | direct `api.deepseek.com` |
| `opencode/deepseek-v4-flash` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/deepseek-v4-pro` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/kimi-k2.7-code` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/glm-5.3-flash` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/mimo-v2.5-free` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/minimax-m3` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/qwen3.6-plus` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/nemotron-3.5-lightning-free` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | flat | OpenCode Go plan |
| `opencode/claude-*` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | metered credit | OpenCode Zen; independent second reads only |
| `opencode/gpt-*` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | metered credit | OpenCode Zen; independent second reads only |
| `opencode/gemini-*` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | metered credit | OpenCode Zen; independent second reads only |
| `opencode/grok-*` | `opencode` | `~/.local/share/opencode/auth.json`, key under `opencode` | metered credit | OpenCode Zen; independent second reads only |
| `inception/mercury-2.5` | `inception` | provider block `inception` | metered | Inception; fast |
| `ollama/north-mini-code-32k` | `ollama` | keyless, `127.0.0.1:11434` | — | local; one slot; never a bench; the harness does not parse its tool calls (#591) |
| `ollama/laguna` tags | `ollama` | keyless, `127.0.0.1:11434` | — | local; one slot; never a bench; the harness does not parse its tool calls (#591) |
| `ollama/granite` tags | `ollama` | keyless, `127.0.0.1:11434` | — | local; one slot; never a bench; the harness does not parse its tool calls (#591) |

## The rules

These rules keep the registry honest, so the routes are never forgotten again:

1. **Cheapest first.** Flat routes run before metered ones, and a route listed
   twice gets twice the share. The routing lives in a routes file per card
   class.
2. **Probe before real cards.** Every route carries real cards only after a
   known-answer probe: `nova-swarm native` with the probe card. That is the probe
   verb for every route here.
3. **The provider block comes from this registry.** A provider block is
   regenerated from this file and never dropped by a config rewrite — #479, and
   #586 dropped deepseek. Never again.
4. **Keys are owned.** A key file is never read by a tool or a person other than
   the owner.
5. **Space keys come by hand.** Space gets its key files by Glenn's hand.