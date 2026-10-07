# nova-local dogfood report

Testing nova-local as a stranger would: help, then every verb with real flags.

1. `nova-local -h`
   Printed:
   ```
   nova-local: run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts

   how it works: an engine (ollama) serves models from the shared store, <ai-root>/shared/models.
   status reads each engine and the box; serve makes <name>-<ctx>k, the context baked in, and loads it;
   ```
   Expected: banner answering what, how, how to use; examples that run.
   Grade: URGENT

2. `nova-local status`
   Printed: STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve
   Expected: engine status lines or clear refusal with remedy.
   Grade: NEXT

3. `nova-local serve --engine ollama --model gemma4-12b --num-ctx 32768 --seed 7`
   Printed: SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve
   Expected: serve output or clear refusal.
   Grade: NEXT

4. `nova-local version`
   Printed: nova-local devel linux/amd64 go1.26.6
   Expected: version line.
   Grade: NEXT

5. `nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /tmp/test --key-file /tmp/key --usage opencode --deadline 20m`
   Printed: WORKER REFUSED: --env-var is required; it wants the NAME of the variable the provider reads (OLLAMA_API_KEY), never a value; refusing to guess; run: nova-local help
   Expected: worker JSON or clear refusal.
   Grade: URGENT

READ 8/10 — help is clear, examples runnable, refusals carry remedies.
USE 7/10 — verbs are well-documented, dry-run mode present, but requires running daemon.

urgent=2 next=3
