# Model routes — every route a bench can run

This is the registry of every model route a bench can run: the provider, the key
location by path, the model ids and the cost class. A route is real only when it
is here.

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
| `ollama/north-mini-code-32k` | `ollama` | keyless, `127.0.0.1:11434` | — | local; one slot; never a bench; the harness does not parse its tool calls |
| `ollama/laguna` tags | `ollama` | keyless, `127.0.0.1:11434` | — | local; one slot; never a bench; the harness does not parse its tool calls |
| `ollama/granite` tags | `ollama` | keyless, `127.0.0.1:11434` | — | local; one slot; never a bench; the harness does not parse its tool calls |

## The rules

1. **Cheapest first.** Flat routes run before metered ones.
2. **The provider block comes from this registry.** A harness's provider block is
   written from this file, and a config rewrite never drops one.
3. **Keys are owned.** A key file is never read by a tool or a person other than
   the owner.
4. **Space keys come by hand.** Space gets its key files by hand.
